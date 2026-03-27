// Tests for default config path resolution.
//
// Architected and Developed By:- Faisal Hanif | imfanee@gmail.com
package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFirstExistingDefaultConfigPathAndResolve(t *testing.T) {
	td := t.TempDir()
	p := filepath.Join(td, "found.toml")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := defaultConfigPathCandidates
	defaultConfigPathCandidates = []string{p}
	defer func() { defaultConfigPathCandidates = old }()

	if got := FirstExistingDefaultConfigPath(); got != p {
		t.Fatalf("FirstExisting: got %q want %q", got, p)
	}
	if got := ResolveConfigPathForLoad(""); got != p {
		t.Fatalf("Resolve empty: got %q want %q", got, p)
	}
}
