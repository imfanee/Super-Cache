// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Optional persisted peer list (P2.5).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package peer

import (
	"encoding/json"
	"fmt"
	"os"
)

type peerStateFile struct {
	Peers []string `json:"peers"`
}

// LoadPeerStateFile reads peer addresses from a JSON file written by SavePeerStateFile.
func LoadPeerStateFile(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var st peerStateFile
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse peer state: %w", err)
	}
	return st.Peers, nil
}

// SavePeerStateFile writes the current peer list (600 permissions).
func SavePeerStateFile(path string, peers []string) error {
	st := peerStateFile{Peers: append([]string(nil), peers...)}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
