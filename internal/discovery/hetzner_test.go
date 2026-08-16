// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Tests for listing cluster peers from the Hetzner Cloud API.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package discovery

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// hetznerAPI stands in for the real API, recording what was asked of it.
type hetznerAPI struct {
	srv       *httptest.Server
	authSeen  string
	selectors []string
	pages     []string
}

func newHetznerAPI(t *testing.T, pages ...string) *hetznerAPI {
	t.Helper()
	api := &hetznerAPI{pages: pages}
	api.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		api.authSeen = r.Header.Get("Authorization")
		api.selectors = append(api.selectors, r.URL.Query().Get("label_selector"))
		page := 1
		fmt.Sscanf(r.URL.Query().Get("page"), "%d", &page)
		if page < 1 || page > len(api.pages) {
			http.Error(w, "no such page", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(api.pages[page-1]))
	}))
	t.Cleanup(api.srv.Close)
	return api
}

const onePage = `{
  "servers": [
    {"name":"fs-1","status":"running","private_net":[{"network":4711,"ip":"10.0.0.11"}]},
    {"name":"fs-2","status":"running","private_net":[{"network":4711,"ip":"10.0.0.12"}]}
  ],
  "meta": {"pagination": {"next_page": null}}
}`

func TestHetznerListsPrivateAddresses(t *testing.T) {
	api := newHetznerAPI(t, onePage)
	h := &Hetzner{Token: "secret-token", Port: 7379, BaseURL: api.srv.URL, LabelSelector: "role=supercache"}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.11:7379", "10.0.0.12:7379"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}
	if api.authSeen != "Bearer secret-token" {
		t.Fatalf("token not sent as a bearer credential: %q", api.authSeen)
	}
	if api.selectors[0] != "role=supercache" {
		t.Fatalf("label selector not passed through: %q", api.selectors[0])
	}
}

// TestHetznerSkipsServerWithoutPrivateAddress is a deliberate safety property: a server reachable
// only over the public internet is not made a peer, because replication is plaintext unless peer
// TLS is configured and public traffic is billed.
func TestHetznerSkipsServerWithoutPrivateAddress(t *testing.T) {
	api := newHetznerAPI(t, `{"servers":[
		{"name":"no-private","status":"running","private_net":[]},
		{"name":"ok","status":"running","private_net":[{"network":1,"ip":"10.0.0.5"}]}
	],"meta":{"pagination":{"next_page":null}}}`)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "10.0.0.5:7379" {
		t.Fatalf("expected only the server with a private address, got %v", got)
	}
}

// TestHetznerSkipsServersNotRunning covers instances an autoscaler has created but not started,
// which have no usable address yet and would only waste dials.
func TestHetznerSkipsServersNotRunning(t *testing.T) {
	api := newHetznerAPI(t, `{"servers":[
		{"name":"starting","status":"initializing","private_net":[{"network":1,"ip":"10.0.0.7"}]},
		{"name":"off","status":"off","private_net":[{"network":1,"ip":"10.0.0.8"}]},
		{"name":"up","status":"running","private_net":[{"network":1,"ip":"10.0.0.9"}]}
	],"meta":{"pagination":{"next_page":null}}}`)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "10.0.0.9:7379" {
		t.Fatalf("expected only the running server, got %v", got)
	}
}

func TestHetznerFollowsPagination(t *testing.T) {
	api := newHetznerAPI(t,
		`{"servers":[{"name":"a","status":"running","private_net":[{"network":1,"ip":"10.0.0.1"}]}],"meta":{"pagination":{"next_page":2}}}`,
		`{"servers":[{"name":"b","status":"running","private_net":[{"network":1,"ip":"10.0.0.2"}]}],"meta":{"pagination":{"next_page":null}}}`,
	)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected both pages, got %v", got)
	}
}

// TestHetznerSelectsConfiguredNetwork covers a server on several private networks, where taking
// the wrong one would produce an address no peer can reach.
func TestHetznerSelectsConfiguredNetwork(t *testing.T) {
	api := newHetznerAPI(t, `{"servers":[
		{"name":"multi","status":"running","private_net":[
			{"network":100,"ip":"10.9.9.9"},
			{"network":200,"ip":"10.0.0.4"}
		]}
	],"meta":{"pagination":{"next_page":null}}}`)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL, NetworkID: 200}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "10.0.0.4:7379" {
		t.Fatalf("expected the configured network's address, got %v", got)
	}
}

func TestHetznerReportsAPIFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"unable to authenticate"}}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	h := &Hetzner{Token: "wrong", Port: 7379, BaseURL: srv.URL}
	_, err := h.Peers(context.Background())
	if err == nil {
		t.Fatal("expected an error from a rejected request")
	}
	// The token must never reach a log line through an error string.
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestHetznerRequiresTokenAndPort(t *testing.T) {
	if _, err := (&Hetzner{Port: 7379}).Peers(context.Background()); err == nil {
		t.Fatal("expected an error without a token")
	}
	if _, err := (&Hetzner{Token: "t"}).Peers(context.Background()); err == nil {
		t.Fatal("expected an error without a peer port")
	}
}

func TestHetznerHandlesMalformedResponse(t *testing.T) {
	api := newHetznerAPI(t, `{not json`)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL}
	if _, err := h.Peers(context.Background()); err == nil {
		t.Fatal("expected a decode error")
	}
}

func TestHetznerDeduplicatesAddresses(t *testing.T) {
	api := newHetznerAPI(t, `{"servers":[
		{"name":"a","status":"running","private_net":[{"network":1,"ip":"10.0.0.1"}]},
		{"name":"b","status":"running","private_net":[{"network":1,"ip":"10.0.0.1"}]}
	],"meta":{"pagination":{"next_page":null}}}`)
	h := &Hetzner{Token: "t", Port: 7379, BaseURL: api.srv.URL}
	got, err := h.Peers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one address, got %v", got)
	}
}

func TestHetznerName(t *testing.T) {
	if (&Hetzner{}).Name() != "hetzner" {
		t.Fatal("unexpected provider name")
	}
}
