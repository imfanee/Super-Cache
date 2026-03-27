// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Peer TCP/TLS dial helpers (P6.1).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"context"
	"crypto/tls"
	"net"
	"time"
)

func (s *Service) peerDialWithTimeout(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	d := &net.Dialer{Timeout: timeout}
	if s.peerDialTLS == nil {
		return d.DialContext(ctx, "tcp", addr)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	cfg := s.peerDialTLS.Clone()
	cfg.ServerName = host
	td := tls.Dialer{NetDialer: d, Config: cfg}
	return td.DialContext(ctx, "tcp", addr)
}
