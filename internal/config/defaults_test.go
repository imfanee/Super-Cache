// Tests for default configuration constants.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import "testing"

func TestDefaultConstants(t *testing.T) {
	if DefaultClientPort == DefaultPeerPort {
		t.Fatal("client and peer default ports must differ")
	}
	if DefaultConfigPath != "/etc/supercache/supercache.toml" {
		t.Fatalf("DefaultConfigPath: got %q", DefaultConfigPath)
	}
	if DefaultConfigPathAlt != "/etc/supercache/supercache.conf" {
		t.Fatalf("DefaultConfigPathAlt: got %q", DefaultConfigPathAlt)
	}
}
