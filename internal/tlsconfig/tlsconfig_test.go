// Tests for TLS config helpers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package tlsconfig

import (
	"crypto/tls"
	"testing"
)

func TestParseMinVersion(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want uint16
	}{
		{"", tls.VersionTLS12},
		{"1.2", tls.VersionTLS12},
		{" 1.3 ", tls.VersionTLS13},
	} {
		got, err := ParseMinVersion(tc.in)
		if err != nil {
			t.Fatalf("ParseMinVersion(%q): %v", tc.in, err)
		}
		if got != tc.want {
			t.Fatalf("ParseMinVersion(%q) = %v want %v", tc.in, got, tc.want)
		}
	}
	if _, err := ParseMinVersion("1.1"); err == nil {
		t.Fatal("expected error")
	}
}
