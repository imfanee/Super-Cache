// Tests for supercache-cli helpers.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/supercache/supercache/internal/mgmt"
)

func TestFormatOutputJSONMode(t *testing.T) {
	var buf bytes.Buffer
	err := formatOutputTo(&buf, mgmt.MgmtResponse{OK: true, Data: map[string]any{"uptime_sec": int64(42)}}, true)
	if err != nil {
		t.Fatal(err)
	}
	var resp mgmt.MgmtResponse
	raw := bytes.TrimSpace(buf.Bytes())
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("invalid json %q: %v", raw, err)
	}
	if !resp.OK {
		t.Fatal("expected ok")
	}
}

func TestFormatOutputHuman(t *testing.T) {
	var buf bytes.Buffer
	err := formatOutputTo(&buf, mgmt.MgmtResponse{OK: true, Data: map[string]any{"uptime_sec": int64(7)}}, false)
	if err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, "uptime_sec") {
		t.Fatalf("expected uptime_sec in output: %q", s)
	}
}
