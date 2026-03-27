// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Management command handlers (STATUS, peers, reload, shutdown, debug).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package mgmt

import (
	"encoding/json"
	"strings"
)

// HandlePing runs the PING management command.
func HandlePing(h Handler) MgmtResponse {
	if h == nil {
		return MgmtResponse{OK: false, Error: "ping not supported"}
	}
	return MgmtResponse{OK: true, Data: h.Ping()}
}

// HandleInfo runs the INFO management command.
func HandleInfo(h Handler) MgmtResponse {
	if h == nil {
		return MgmtResponse{OK: false, Error: "info not supported"}
	}
	return MgmtResponse{OK: true, Data: h.Info()}
}

// HandleStatus returns structured node status for management clients.
func HandleStatus(ext ExtendedHandler) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "status not supported"}
	}
	return MgmtResponse{OK: true, Data: ext.Status()}
}

// HandleReloadConfig reloads configuration from disk and returns changed hot-reloadable fields.
func HandleReloadConfig(ext ExtendedHandler) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "reload not supported"}
	}
	changed, err := ext.ReloadConfig()
	if err != nil {
		return MgmtResponse{OK: false, Error: err.Error()}
	}
	return MgmtResponse{OK: true, Data: changed}
}

// HandlePeersList returns configured peers with connection state.
func HandlePeersList(ext ExtendedHandler) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "peers list not supported"}
	}
	return MgmtResponse{OK: true, Data: ext.PeersList()}
}

// HandlePeersAdd adds a runtime peer address.
func HandlePeersAdd(ext ExtendedHandler, args json.RawMessage) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "peers add not supported"}
	}
	var a struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Addr) == "" {
		return MgmtResponse{OK: false, Error: "missing addr in args"}
	}
	if err := ext.PeersAdd(strings.TrimSpace(a.Addr)); err != nil {
		return MgmtResponse{OK: false, Error: err.Error()}
	}
	return MgmtResponse{OK: true, Data: map[string]string{"addr": a.Addr}}
}

// HandlePeersRemove removes a configured peer address.
func HandlePeersRemove(ext ExtendedHandler, args json.RawMessage) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "peers remove not supported"}
	}
	var a struct {
		Addr string `json:"addr"`
	}
	if err := json.Unmarshal(args, &a); err != nil || strings.TrimSpace(a.Addr) == "" {
		return MgmtResponse{OK: false, Error: "missing addr in args"}
	}
	if err := ext.PeersRemove(strings.TrimSpace(a.Addr)); err != nil {
		return MgmtResponse{OK: false, Error: err.Error()}
	}
	return MgmtResponse{OK: true, Data: "OK"}
}

// HandleShutdown requests process shutdown.
func HandleShutdown(ext ExtendedHandler, args json.RawMessage) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "shutdown not supported"}
	}
	graceful := true
	if len(args) > 0 {
		var a struct {
			Graceful *bool `json:"graceful"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Graceful != nil {
			graceful = *a.Graceful
		}
	}
	if err := ext.RequestShutdown(graceful); err != nil {
		return MgmtResponse{OK: false, Error: err.Error()}
	}
	return MgmtResponse{OK: true, Data: "OK"}
}

// HandleDebugKeyspace samples keys with type and TTL information.
func HandleDebugKeyspace(ext ExtendedHandler, args json.RawMessage) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "debug keyspace not supported"}
	}
	maxKeys := 10
	if len(args) > 0 {
		var a struct {
			Count int `json:"count"`
		}
		_ = json.Unmarshal(args, &a)
		if a.Count > 0 {
			maxKeys = a.Count
		}
	}
	m, err := ext.DebugKeyspace(maxKeys)
	if err != nil {
		return MgmtResponse{OK: false, Error: err.Error()}
	}
	return MgmtResponse{OK: true, Data: m}
}

// HandleBootstrapStatus returns bootstrap / replication queue telemetry.
func HandleBootstrapStatus(ext ExtendedHandler) MgmtResponse {
	if ext == nil {
		return MgmtResponse{OK: false, Error: "bootstrap status not supported"}
	}
	return MgmtResponse{OK: true, Data: ext.BootstrapStatus()}
}

// DispatchMgmt routes a validated management command to the appropriate handler.
func DispatchMgmt(h Handler, ext ExtendedHandler, req MgmtRequest) MgmtResponse {
	switch strings.ToUpper(strings.TrimSpace(req.Cmd)) {
	case "PING":
		return HandlePing(h)
	case "INFO":
		return HandleInfo(h)
	case "STATUS":
		return HandleStatus(ext)
	case "RELOAD_CONFIG":
		return HandleReloadConfig(ext)
	case "PEERS_LIST":
		return HandlePeersList(ext)
	case "PEERS_ADD":
		return HandlePeersAdd(ext, req.Args)
	case "PEERS_REMOVE":
		return HandlePeersRemove(ext, req.Args)
	case "SHUTDOWN":
		return HandleShutdown(ext, req.Args)
	case "DEBUG_KEYSPACE":
		return HandleDebugKeyspace(ext, req.Args)
	case "BOOTSTRAP_STATUS":
		return HandleBootstrapStatus(ext)
	default:
		return MgmtResponse{OK: false, Error: "unknown command"}
	}
}
