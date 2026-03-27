// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Optional management commands (P1) beyond PING/INFO.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

// ExtendedHandler is implemented by *server.Server for reload, peers, debug, and shutdown.
type ExtendedHandler interface {
	ReloadConfig() ([]string, error)
	Status() map[string]any
	PeersList() []map[string]any
	PeersAdd(addr string) error
	PeersRemove(addr string) error
	DebugKeyspace(maxKeys int) (map[string]any, error)
	BootstrapStatus() map[string]any
	RequestShutdown(graceful bool) error
}
