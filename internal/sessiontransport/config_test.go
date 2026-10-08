package sessiontransport

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestConfigurationDiscoveryChecksStorageOnceAroundAllReads(t *testing.T) {
	d, f, workspace, target := transportFixture(t)
	// An empty, valid configuration must still search every possible layer.
	f.configArchive = configArchive(t, tar.TypeReg, false)
	f.configArchive = bytes.ReplaceAll(f.configArchive, []byte("sqlite_home"), []byte("unused_home"))
	result, err := d.ResolveSQLiteHome(context.Background(), workspace, target, "")
	if err != nil || result.Path != target.CodexHome {
		t.Fatalf("discovery: %+v %v", result, err)
	}
	counts := map[string]int{}
	for _, args := range f.commands {
		counts[args[1]]++
	}
	if counts["cp"] != 6 || counts["inspect"] != 2 || counts["volume"] != 2 || len(f.commands) != 10 {
		t.Fatalf("configuration discovery amplified Docker work: %v", counts)
	}
}

func TestConfigurationDiscoveryRejectsChangesBeforeReturning(t *testing.T) {
	for _, change := range []string{"identity", "volume", "cancel"} {
		t.Run(change, func(t *testing.T) {
			d, f, workspace, target := transportFixture(t)
			f.configArchive = configArchive(t, tar.TypeReg, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.changeAfterCopy = func() {
				switch change {
				case "identity":
					f.workstation.Config.Env[0] = "HCORRAL_HOST_UID=1000"
				case "volume":
					f.volume = nil
				case "cancel":
					cancel()
				}
			}
			if _, err := d.ResolveSQLiteHome(ctx, workspace, target, ""); err == nil {
				t.Fatal("accepted configuration from changed or cancelled storage")
			}
			for _, args := range f.commands {
				if strings.Contains(" create start rm stop ", " "+args[1]+" ") {
					t.Fatalf("discovery mutated runtime: %q", args)
				}
			}
		})
	}
}

func configArchive(t *testing.T, kind byte, extra bool) []byte {
	t.Helper()
	var buffer bytes.Buffer
	w := tar.NewWriter(&buffer)
	data := []byte("sqlite_home = '/separate state'\n")
	header := &tar.Header{Name: "config.toml", Typeflag: kind, Mode: 0o600}
	if kind == tar.TypeReg {
		header.Size = int64(len(data))
	}
	if kind == tar.TypeSymlink {
		header.Linkname = "/elsewhere"
	}
	if err := w.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if extra {
		if err := w.WriteHeader(&tar.Header{Name: "unexpected", Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestReadConfigUsesActualContainerWithoutStartingAnything(t *testing.T) {
	for _, state := range []string{"running", "exited"} {
		t.Run(state, func(t *testing.T) {
			d, f, _, target := transportFixture(t)
			f.workstation.State.Status, f.workstation.State.Running = state, state == "running"
			f.configArchive = configArchive(t, tar.TypeReg, false)
			data, err := d.readConfig(context.Background(), target.ContainerID, "/etc/codex/config.toml")
			if err != nil || string(data) != "sqlite_home = '/separate state'\n" {
				t.Fatalf("config: %q %v", data, err)
			}
			for _, args := range f.commands {
				if args[1] != "inspect" && args[1] != "volume" && args[1] != "cp" {
					t.Fatalf("read config mutated runtime: %q", args)
				}
			}
		})
	}
}

func TestReadConfigDistinguishesAbsentLayerFromFailedInspection(t *testing.T) {
	for _, problem := range []string{"missing", "container gone", "permission", "transport", "directory", "symlink", "extra member", "truncated", "oversized"} {
		t.Run(problem, func(t *testing.T) {
			d, f, _, target := transportFixture(t)
			path := "/etc/codex/config.toml"
			f.configErr = errors.New("Docker copy failed")
			switch problem {
			case "missing":
				f.configStderr = []byte("Error response from daemon: Could not find the file " + path + " in container " + target.ContainerID + "\n")
			case "container gone":
				f.configStderr = []byte("No such container: " + target.ContainerID)
			case "permission":
				f.configStderr = []byte("permission denied")
			case "transport":
				f.configStderr = []byte("Cannot connect to the Docker daemon")
			case "directory":
				f.configErr = nil
				f.configArchive = configArchive(t, tar.TypeDir, false)
			case "symlink":
				f.configErr = nil
				f.configArchive = configArchive(t, tar.TypeSymlink, false)
			case "extra member":
				f.configErr = nil
				f.configArchive = configArchive(t, tar.TypeReg, true)
			case "truncated":
				f.configErr = nil
				f.configArchive = configArchive(t, tar.TypeReg, false)[:520]
			case "oversized":
				f.configErr = nil
				f.configArchive = make([]byte, maximumConfigBytes+128<<10)
			}
			if _, err := d.readConfig(context.Background(), target.ContainerID, path); err == nil || errors.Is(err, os.ErrNotExist) != (problem == "missing") {
				t.Fatalf("incorrect missing-layer classification: %v", err)
			}
		})
	}
}
