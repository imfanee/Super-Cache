// Tests for the client_idle_timeout configuration key.
package config

import "testing"

func TestClientIdleTimeoutDefaults(t *testing.T) {
	cfg := &Config{}
	ApplyDefaults(cfg)
	if cfg.ClientIdleTimeout != DefaultClientIdleTimeout {
		t.Fatalf("default: got %d want %d", cfg.ClientIdleTimeout, DefaultClientIdleTimeout)
	}

	cfg = &Config{ClientIdleTimeout: -1}
	ApplyDefaults(cfg)
	if cfg.ClientIdleTimeout != -1 {
		t.Fatalf("negative (disabled) must be preserved: got %d", cfg.ClientIdleTimeout)
	}

	cfg = &Config{ClientIdleTimeout: 120}
	ApplyDefaults(cfg)
	if cfg.ClientIdleTimeout != 120 {
		t.Fatalf("explicit value must be preserved: got %d", cfg.ClientIdleTimeout)
	}
}

func TestClientIdleTimeoutHotReloadable(t *testing.T) {
	a := &Config{ClientIdleTimeout: 3600}
	b := &Config{ClientIdleTimeout: 600}
	hot, blocked := diffConfigs(a, b)
	if len(blocked) != 0 {
		t.Fatalf("unexpected blocked fields: %v", blocked)
	}
	found := false
	for _, f := range hot {
		if f == "client_idle_timeout" {
			found = true
		}
	}
	if !found {
		t.Fatalf("client_idle_timeout not in hot list: %v", hot)
	}
}
