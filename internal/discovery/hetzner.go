// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Peer discovery from the Hetzner Cloud server inventory.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultHetznerBaseURL is the Hetzner Cloud API root.
const DefaultHetznerBaseURL = "https://api.hetzner.cloud"

// maxHetznerPages bounds pagination so a malformed or hostile response cannot loop forever.
const maxHetznerPages = 40

// hetznerPageSize is the number of servers requested per call; 50 is the API maximum.
const hetznerPageSize = 50

// Hetzner lists cluster peers from the Hetzner Cloud API.
//
// This exists because the alternatives do not work on this infrastructure. Hetzner Cloud private
// networks are routed rather than switched, so a broadcast never reaches another node, and the
// only other inventory of the fleet is maintained by hand. The autoscaler already labels the
// instances it creates, which makes the API the one source that is both automatic and current.
type Hetzner struct {
	// Token authenticates to the API. A read-only token is sufficient and is what should be
	// used: this only ever lists servers.
	Token string
	// LabelSelector restricts the listing, for example "role=supercache". Empty lists every
	// server in the project, which is rarely what is wanted.
	LabelSelector string
	// NetworkID, when non-zero, selects which private network's address to use on a server
	// attached to several. Zero uses the first one reported.
	NetworkID int64
	// Port is the peer port to pair with each discovered address, since the API reports
	// addresses only. This assumes the fleet shares one peer port, which is the normal
	// deployment and the same assumption made when learning a peer from its source address.
	Port int
	// BaseURL overrides the API root, for tests.
	BaseURL string
	// HTTP overrides the client, for tests.
	HTTP *http.Client
}

// hetznerServer is the part of a server object this cares about.
type hetznerServer struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	PrivateNet []struct {
		Network int64  `json:"network"`
		IP      string `json:"ip"`
	} `json:"private_net"`
}

type hetznerListResponse struct {
	Servers []hetznerServer `json:"servers"`
	Meta    struct {
		Pagination struct {
			NextPage int `json:"next_page"`
		} `json:"pagination"`
	} `json:"meta"`
}

// Name identifies this provider in logs.
func (h *Hetzner) Name() string { return "hetzner" }

func (h *Hetzner) baseURL() string {
	if strings.TrimSpace(h.BaseURL) != "" {
		return strings.TrimRight(strings.TrimSpace(h.BaseURL), "/")
	}
	return DefaultHetznerBaseURL
}

func (h *Hetzner) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// Peers lists running servers and returns the peer address of each.
//
// Only private addresses are used. A server with no private address is skipped rather than
// reached over its public one: replication is plaintext unless peer TLS is configured, and
// carrying it over the public internet would also be billed as egress.
func (h *Hetzner) Peers(ctx context.Context) ([]string, error) {
	if strings.TrimSpace(h.Token) == "" {
		return nil, fmt.Errorf("hetzner discovery: no api token")
	}
	if h.Port <= 0 {
		return nil, fmt.Errorf("hetzner discovery: no peer port")
	}
	var out []string
	seen := make(map[string]struct{})
	skippedNoPrivate := 0
	page := 1
	for pages := 0; pages < maxHetznerPages && page > 0; pages++ {
		resp, err := h.listPage(ctx, page)
		if err != nil {
			return nil, err
		}
		for _, srv := range resp.Servers {
			// A server still being created has no usable address yet, and one that is off
			// cannot answer; both reappear on a later poll.
			if srv.Status != "running" {
				continue
			}
			ip := h.privateIP(srv)
			if ip == "" {
				skippedNoPrivate++
				continue
			}
			addr := net.JoinHostPort(ip, strconv.Itoa(h.Port))
			if _, dup := seen[addr]; dup {
				continue
			}
			seen[addr] = struct{}{}
			out = append(out, addr)
		}
		page = resp.Meta.Pagination.NextPage
	}
	if skippedNoPrivate > 0 {
		slog.Warn("hetzner discovery skipped servers with no private address; "+
			"they cannot be cluster peers without exposing replication to the public internet",
			"skipped", skippedNoPrivate)
	}
	return out, nil
}

// privateIP picks the address to use for a server, honouring NetworkID when one is configured.
func (h *Hetzner) privateIP(srv hetznerServer) string {
	for _, n := range srv.PrivateNet {
		ip := strings.TrimSpace(n.IP)
		if ip == "" {
			continue
		}
		if h.NetworkID != 0 && n.Network != h.NetworkID {
			continue
		}
		return ip
	}
	return ""
}

func (h *Hetzner) listPage(ctx context.Context, page int) (*hetznerListResponse, error) {
	q := url.Values{}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(hetznerPageSize))
	if sel := strings.TrimSpace(h.LabelSelector); sel != "" {
		q.Set("label_selector", sel)
	}
	endpoint := h.baseURL() + "/v1/servers?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("hetzner discovery: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+h.Token)
	req.Header.Set("Accept", "application/json")

	res, err := h.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("hetzner discovery: %w", err)
	}
	defer res.Body.Close()

	// Bounded so a wrong endpoint returning something enormous cannot exhaust memory.
	body, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("hetzner discovery: read body: %w", err)
	}
	if res.StatusCode != http.StatusOK {
		// The body may name the reason, but it can also echo request details, so only the
		// status is reported. A token must never reach the logs.
		return nil, fmt.Errorf("hetzner discovery: api returned %s", res.Status)
	}
	var parsed hetznerListResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("hetzner discovery: decode response: %w", err)
	}
	return &parsed, nil
}
