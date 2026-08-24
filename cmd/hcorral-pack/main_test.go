package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveIsDeterministic(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	input := filepath.Join(directory, "hcorral")
	if err := os.WriteFile(input, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	one, two := filepath.Join(directory, "one.tar.gz"), filepath.Join(directory, "two.tar.gz")
	args := []string{"-mtime", "315532800", "-file", input + "=hcorral"}
	if err := archive(append([]string{"-output", one}, args...)); err != nil {
		t.Fatal(err)
	}
	if err := archive(append([]string{"-output", two}, args...)); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(one)
	b, _ := os.ReadFile(two)
	if !bytes.Equal(a, b) {
		t.Fatal("archives differ")
	}
}
