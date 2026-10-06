package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

// Prepare both helper payloads before compiling the launcher that embeds them.
// Validate actual executable linkage/architecture and compress deterministically.
func helpers(args []string) error {
	flags := flag.NewFlagSet("helpers", flag.ContinueOnError)
	amd64 := flags.String("amd64", "", "Linux AMD64 helper executable")
	arm64 := flags.String("arm64", "", "Linux ARM64 helper executable")
	output := flags.String("output", "", "helper payload directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *amd64 == "" || *arm64 == "" || *output == "" {
		return errors.New("helpers requires -amd64, -arm64 and -output")
	}
	payloads := make(map[string][]byte)
	for _, entry := range []struct{ arch, path string }{{"amd64", *amd64}, {"arm64", *arm64}} {
		if err := linkage([]string{"-os", "linux", "-arch", entry.arch, entry.path}); err != nil {
			return fmt.Errorf("validate %s helper: %w", entry.arch, err)
		}
		data, err := os.ReadFile(entry.path)
		if err != nil {
			return err
		}
		if len(data) > 128<<20 {
			return errors.New("helper exceeds embedded payload size limit")
		}
		var out bytes.Buffer
		w, err := gzip.NewWriterLevel(&out, gzip.BestCompression)
		if err != nil {
			return err
		}
		w.Header.OS = 255
		if _, err := w.Write(data); err != nil {
			return err
		}
		if err := w.Close(); err != nil {
			return err
		}
		payloads[entry.arch] = out.Bytes()
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	for _, arch := range []string{"amd64", "arm64"} {
		if err := writeHelperPayload(*output, "linux-"+arch+".gz", payloads[arch]); err != nil {
			return err
		}
	}
	return nil
}

func writeHelperPayload(directory, name string, data []byte) error {
	f, err := os.CreateTemp(directory, ".helper-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(0o644); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(directory, name))
}
