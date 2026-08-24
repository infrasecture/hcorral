//go:build linux

package gui

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

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
