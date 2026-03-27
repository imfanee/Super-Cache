// Copyright (c) 2024-2026 Faisal Hanif. All rights reserved.
// Use and modification governed by the Super-Cache Software Licence.
// Contact: imfanee@gmail.com
// Default config path resolution (FR-010: supercache.toml primary, supercache.conf alternate).
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"os"
	"strings"
)

// defaultConfigPathCandidates is the ordered list of well-known config paths (FR-010).
// It is a package variable so tests can temporarily override the search list.
var defaultConfigPathCandidates = []string{DefaultConfigPath, DefaultConfigPathAlt}

// FirstExistingDefaultConfigPath returns the first default path that exists as a regular file, or "" if none.
func FirstExistingDefaultConfigPath() string {
	for _, p := range defaultConfigPathCandidates {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

// ResolveConfigPathForLoad returns explicit when non-empty; otherwise the first existing default config file; if none exist, DefaultConfigPath (for consistent error messages).
func ResolveConfigPathForLoad(explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if p := FirstExistingDefaultConfigPath(); p != "" {
		return p
	}
	return DefaultConfigPath
}
