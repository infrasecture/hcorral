package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeWorkdirMapsLogicalWorkspaceToPhysicalPath(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	logical := filepath.Join(root, "logical")
	if err := os.MkdirAll(filepath.Join(physical, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(physical, logical); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeWorkdir(filepath.Join(logical, "existing"), logical, "/home/runtime", physical)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(physical, "existing"); got != want {
		t.Fatalf("workdir = %q, want %q", got, want)
	}
}

func TestNormalizeWorkdirRejectsUnmountedAndEscapingPaths(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "physical")
	outside := filepath.Join(root, "outside")
	if err := os.MkdirAll(physical, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeWorkdir(outside, physical, "/home/runtime", physical); err == nil {
		t.Fatal("unmounted workdir was accepted")
	}
	if err := os.Symlink(outside, filepath.Join(physical, "escape")); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeWorkdir(filepath.Join(physical, "escape", "child"), physical, "/home/runtime", physical); err == nil {
		t.Fatal("symlink-escaping workdir was accepted")
	}
	if _, err := normalizeWorkdir(filepath.Join(physical, "missing"), physical, "/home/runtime", physical); err == nil {
		t.Fatal("missing workspace workdir was accepted")
	}
	if got, err := normalizeWorkdir("/home/runtime/project", physical, "/home/runtime", physical); err != nil || got != "/home/runtime/project" {
		t.Fatalf("home workdir = %q, %v", got, err)
	}
}
