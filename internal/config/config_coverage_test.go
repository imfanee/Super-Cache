// Additional validation and reload coverage for Super-Cache config.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateLogFormat(t *testing.T) {
	c := minimalValidConfig()
	c.LogFormat = "bad"
	if err := c.Validate(); err == nil {
		t.Fatal("expected log_format error")
	}
	c.LogFormat = "json"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestValidateMetricsPort(t *testing.T) {
	c := minimalValidConfig()
	c.MetricsPort = 70000
	if err := c.Validate(); err == nil {
		t.Fatal("expected metrics port error")
	}
	c = minimalValidConfig()
	c.MetricsPort = 6379
	if err := c.Validate(); err == nil {
		t.Fatal("metrics equals client")
	}
	c = minimalValidConfig()
	c.MetricsPort = 7379
	if err := c.Validate(); err == nil {
		t.Fatal("metrics equals peer")
	}
}

func TestValidateMgmtTCP(t *testing.T) {
	c := minimalValidConfig()
	c.MgmtTCPPort = 9000
	c.MgmtTCPBind = "127.0.0.1"
	c.MetricsPort = 9000
	if err := c.Validate(); err == nil {
		t.Fatal("mgmt equals metrics")
	}
	c = minimalValidConfig()
	c.MgmtTCPBind = "127.0.0.1"
	if err := c.Validate(); err == nil {
		t.Fatal("mgmt bind without port")
	}
}

func TestValidateReadableFileDir(t *testing.T) {
	td := t.TempDir()
	c := minimalValidConfig()
	c.ClientTLSCertFile = td
	c.ClientTLSKeyFile = filepath.Join(td, "k.pem")
	if err := os.WriteFile(c.ClientTLSKeyFile, []byte("k"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil {
		t.Fatal("cert path is directory")
	}
}

func TestValidatePeerAddrEdge(t *testing.T) {
	if err := ValidatePeerAddr(":6379"); err == nil {
		t.Fatal("empty host")
	}
	if err := ValidatePeerAddr("host:abc"); err == nil {
		t.Fatal("bad port")
	}
}

func TestParseMemoryBytesSuffixEdge(t *testing.T) {
	if _, err := parseMemoryBytes("10xx"); err == nil {
		t.Fatal("bad suffix")
	}
	if _, err := parseMemoryBytes("999999999999999999999tb"); err == nil {
		t.Fatal("overflow")
	}
}

func TestReloadLogOutputAndFormatHot(t *testing.T) {
	sec := strings.Repeat("l", 32)
	p1 := writeToml(t, `shared_secret = "`+sec+`"
log_output = "stdout"
log_format = "text"
`)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	td := t.TempDir()
	logPath := filepath.Join(td, "a.log")
	p2 := writeToml(t, `shared_secret = "`+sec+`"
log_output = "`+logPath+`"
log_format = "json"
`)
	_, changed, err := Reload(cur, p2)
	if err != nil {
		t.Fatal(err)
	}
	set := map[string]struct{}{}
	for _, f := range changed {
		set[f] = struct{}{}
	}
	if _, ok := set["log_output"]; !ok {
		t.Fatalf("changed: %v", changed)
	}
	if _, ok := set["log_format"]; !ok {
		t.Fatalf("changed: %v", changed)
	}
}

func TestReloadMaxMemoryPolicyHot(t *testing.T) {
	sec := strings.Repeat("n", 32)
	p1 := writeToml(t, `shared_secret = "`+sec+`"
max_memory_policy = "noeviction"
`)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := writeToml(t, `shared_secret = "`+sec+`"
max_memory_policy = "allkeys-lru"
`)
	_, changed, err := Reload(cur, p2)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, f := range changed {
		if f == "max_memory_policy" {
			found = true
		}
	}
	if !found {
		t.Fatalf("changed: %v", changed)
	}
}

func TestReloadHeartbeatHot(t *testing.T) {
	sec := strings.Repeat("h", 32)
	p1 := writeToml(t, `shared_secret = "`+sec+`"
heartbeat_interval = 5
heartbeat_timeout = 20
`)
	cur, err := Load(p1)
	if err != nil {
		t.Fatal(err)
	}
	p2 := writeToml(t, `shared_secret = "`+sec+`"
heartbeat_interval = 6
heartbeat_timeout = 25
`)
	_, changed, err := Reload(cur, p2)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) < 2 {
		t.Fatalf("changed: %v", changed)
	}
}
