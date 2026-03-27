// Reload blocked-field coverage via diffConfigs.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReloadBlockedIndividualFields(t *testing.T) {
	sec := strings.Repeat("b", 32)
	base := `shared_secret = "` + sec + `"
client_port = 6379
peer_port = 7379
`
	cases := []struct {
		name   string
		extra2 string
		want   string
	}{
		{"client_bind", `client_bind = "10.0.0.9"`, "client_bind"},
		{"peer_bind", `peer_bind = "10.0.0.8"`, "peer_bind"},
		{"shared_secret", `shared_secret = "` + strings.Repeat("c", 32) + `"`, "shared_secret"},
		{"mgmt_socket", `mgmt_socket = "/tmp/other.sock"`, "mgmt_socket"},
		{"metrics", `metrics_port = 9123`, "metrics_port"},
		{"gossip", `gossip_peers = true`, "gossip_peers"},
		{"peer_state", `peer_state_file = "/tmp/p.json"`, "peer_state_file"},
		{"repl_spill", `repl_shutdown_spill_path = "/tmp/spill.json"`, "repl_shutdown_spill_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p1 := writeToml(t, base)
			cur, err := Load(p1)
			if err != nil {
				t.Fatal(err)
			}
			p2 := writeToml(t, base+tc.extra2)
			_, _, err = Reload(cur, p2)
			if err == nil {
				t.Fatal("expected blocked reload")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err %v want %q", err, tc.want)
			}
		})
	}
}

func TestReloadBlockedTLSFields(t *testing.T) {
	sec := strings.Repeat("t", 32)
	td := t.TempDir()
	cc := filepath.Join(td, "c.pem")
	ck := filepath.Join(td, "k.pem")
	if err := os.WriteFile(cc, []byte("c"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ck, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := `shared_secret = "` + sec + `"
`
	p1 := writeToml(t, base)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := writeToml(t, base+`
client_tls_cert_file = "`+cc+`"
client_tls_key_file = "`+ck+`"
`)
	_, _, err = Reload(cur, p2)
	if err == nil {
		t.Fatal("expected blocked client tls")
	}
	if !strings.Contains(err.Error(), "client_tls") {
		t.Fatalf("%v", err)
	}
}
