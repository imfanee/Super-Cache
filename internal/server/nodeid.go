// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Node identity helpers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package server

import (
	"crypto/rand"
	"encoding/hex"
)

func randomNodeID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
