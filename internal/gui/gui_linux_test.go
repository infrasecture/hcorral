//go:build linux

package gui

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type contextRunner struct{ endpoint string }

func (r contextRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	if len(argv) == 5 && argv[0] == "docker" && argv[1] == "context" {
		return command.Result{Stdout: []byte(r.endpoint + "\n")}, nil
	}
	return command.Result{}, errors.New("unexpected capture")
}
func (contextRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}
func (contextRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

func TestWaylandRequiresOneOwnedSocket(t *testing.T) {
	directory := t.TempDir()
	socket := filepath.Join(directory, "wayland-0")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	env := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "XDG_RUNTIME_DIR": directory}
	resolver := Resolver{Environ: func(key string) string { return env[key] }, UID: os.Getuid()}
	selection, err := resolver.Resolve(context.Background(), config.GUIIntent{Specified: true, Mode: "wayland"}, identity.Workspace{}, compose.AssetPaths{Wayland: "wayland.yaml"})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Mode != "wayland" || selection.File != "wayland.yaml" || selection.Env["HCORRAL_WAYLAND_SOCKET"] != socket {
		t.Fatalf("selection=%#v", selection)
	}
}

func TestWaylandRejectsPathTraversal(t *testing.T) {
	resolver := Resolver{Environ: func(key string) string {
		if key == "WAYLAND_DISPLAY" {
			return "nested/socket"
		}
		return t.TempDir()
	}, UID: os.Getuid()}
	if _, err := resolver.Resolve(context.Background(), config.GUIIntent{Specified: true, Mode: "wayland"}, identity.Workspace{}, compose.AssetPaths{}); err == nil {
		t.Fatal("expected rejection")
	}
}

func TestSecureStateDirectoryRejectsSymlinkComponent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "hcorral")); err != nil {
		t.Fatal(err)
	}
	if err := secureStateDirectory(root, "hcorral", "gui", "workspace"); err == nil {
		t.Fatal("symlinked state component was accepted")
	}
}

func TestGUIRejectsRemoteDockerContextBeforeSocketInspection(t *testing.T) {
	t.Parallel()
	resolver := Resolver{Runner: contextRunner{endpoint: "ssh://docker.example"}, Environ: func(string) string { return "" }, UID: os.Getuid()}
	_, err := resolver.Resolve(context.Background(), config.GUIIntent{Specified: true, Mode: "x11"}, identity.Workspace{}, compose.AssetPaths{})
	if err == nil || !strings.Contains(err.Error(), "local Unix-socket Docker daemon") {
		t.Fatalf("remote context error = %v", err)
	}
}

var _ command.Runner = contextRunner{}
