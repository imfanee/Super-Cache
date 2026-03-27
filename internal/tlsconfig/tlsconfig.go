// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// TLS helpers for optional client and peer listeners (P6.1, NFR-021).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"
)

// ParseMinVersion maps config strings to TLS version constants. Empty or "1.2" → TLS 1.2; "1.3" → TLS 1.3.
func ParseMinVersion(s string) (uint16, error) {
	v := strings.TrimSpace(strings.ToLower(s))
	if v == "" || v == "1.2" {
		return tls.VersionTLS12, nil
	}
	if v == "1.3" {
		return tls.VersionTLS13, nil
	}
	return 0, fmt.Errorf("tls min version must be 1.2 or 1.3")
}

// LoadServerTLS builds a *tls.Config for accepting TLS connections (Redis client or peer listener).
func LoadServerTLS(certFile, keyFile string, minVersion uint16) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("load tls certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   minVersion,
		Certificates: []tls.Certificate{cert},
	}, nil
}

// LoadClientTLS builds a *tls.Config for dialing TLS peers (RootCAs only). Set ServerName per connection.
func LoadClientTLS(caFile string, minVersion uint16) (*tls.Config, error) {
	pemData, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read tls CA file: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("tls CA file: no PEM certificates found")
	}
	return &tls.Config{
		MinVersion: minVersion,
		RootCAs:    pool,
	}, nil
}
