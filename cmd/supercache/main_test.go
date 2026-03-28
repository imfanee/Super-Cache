package main

import (
	"os"
	"os/exec"
	"testing"
)

func TestVersionFlag(t *testing.T) {
	// Build the binary and run with -version.
	bin := t.TempDir() + "/supercache-test"
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	cmd := exec.Command(bin, "-version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("version flag failed: %v\n%s", err, out)
	}
	if len(out) == 0 {
		t.Fatal("expected version output")
	}
}

func TestNoConfigExitsNonZero(t *testing.T) {
	bin := t.TempDir() + "/supercache-test"
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = "."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(), "SUPERCACHE_CONFIG=")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit when no config provided")
	}
}
