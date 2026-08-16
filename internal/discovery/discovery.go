// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Package discovery finds cluster peers from sources outside the configuration file.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package discovery

import "context"

// Provider reports addresses that may be cluster peers.
//
// A provider answers "which machines exist right now", not "which of them are reachable" or
// "which are in this cluster": both of those are settled afterwards by dialing the address and
// authenticating. Returning an address that turns out to be wrong therefore costs a failed dial
// and nothing more, which is why a provider may be liberal about what it reports.
type Provider interface {
	// Name identifies the provider in logs.
	Name() string
	// Peers returns candidate host:port addresses. The caller filters out its own address and
	// any it already knows.
	Peers(ctx context.Context) ([]string, error)
}
