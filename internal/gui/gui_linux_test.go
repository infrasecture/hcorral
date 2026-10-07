//go:build linux

package gui

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

type desktopRunner struct {
	endpoint, operatingSystem string
	calls                     [][]string
	runs                      int
}

func (r *desktopRunner) Capture(ctx context.Context, argv, _ []string) (command.Result, error) {
	if _, bounded := ctx.Deadline(); !bounded {
		return command.Result{}, errors.New("desktop probe is not bounded")
	}
	r.calls = append(r.calls, argv)
	switch {
	case len(argv) > 2 && argv[0] == "docker" && argv[1] == "context":
		return command.Result{Stdout: []byte(r.endpoint)}, nil
	case len(argv) > 2 && argv[0] == "docker" && argv[1] == "info":
		return command.Result{Stdout: []byte(r.operatingSystem)}, nil
	case len(argv) > 1 && argv[0] == "xauth" && argv[1] == "nlist":
		return command.Result{Stdout: []byte("0100 synthetic-test-credential\n")}, nil
	}
	return command.Result{}, errors.New("unexpected capture")
}

func (r *desktopRunner) Run(_ context.Context, argv, _ []string, input io.Reader, _, _ io.Writer) error {
	r.runs++
	if len(argv) != 5 || argv[0] != "xauth" || argv[1] != "-f" || argv[3] != "nmerge" {
		return errors.New("unexpected mutation")
	}
	content, err := io.ReadAll(input)
	if err != nil {
		return err
	}
	return os.WriteFile(argv[2], content, 0600)
}
func (*desktopRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

func TestDefaultDiscoveryAndDaemonBoundary(t *testing.T) {
	directory := t.TempDir()
	listener, err := net.Listen("unix", filepath.Join(directory, "wayland-0"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for _, tc := range []struct {
		name                            string
		env                             map[string]string
		endpoint, operatingSystem, want string
		explicit                        bool
		wantErr                         bool
		contextName                     string
	}{
		{name: "native default", endpoint: "unix:///var/run/docker.sock", operatingSystem: "Ubuntu 24.04", want: "wayland"},
		{name: "SSH_CONNECTION", env: map[string]string{"SSH_CONNECTION": "remote"}, want: "none"},
		{name: "SSH_CLIENT", env: map[string]string{"SSH_CLIENT": "remote"}, want: "none"},
		{name: "SSH_TTY", env: map[string]string{"SSH_TTY": "/dev/pts/1"}, want: "none"},
		{name: "explicit server local over SSH", env: map[string]string{"SSH_CONNECTION": "remote"}, endpoint: "unix:///var/run/docker.sock", operatingSystem: "Debian", explicit: true, want: "wayland"},
		{name: "remote host overrides stored local context", env: map[string]string{"DOCKER_HOST": "ssh://remote"}, endpoint: "unix:///var/run/docker.sock", want: "none"},
		{name: "context overrides remote host", env: map[string]string{"DOCKER_HOST": "ssh://remote", "DOCKER_CONTEXT": "native"}, endpoint: "unix:///run/user/1000/docker.sock", operatingSystem: "Debian", contextName: "native", want: "wayland"},
		{name: "remote context overrides local host", env: map[string]string{"DOCKER_HOST": "unix:///var/run/docker.sock", "DOCKER_CONTEXT": "remote"}, endpoint: "ssh://remote", contextName: "remote", want: "none"},
		{name: "desktop Unix socket is not native", endpoint: "unix:///home/user/.docker/desktop/docker.sock", operatingSystem: "Docker Desktop", want: "none"},
		{name: "unverified engine", endpoint: "unix:///var/run/docker.sock", want: "none"},
		{name: "explicit remote fails", env: map[string]string{"DOCKER_HOST": "tcp://remote:2376"}, explicit: true, wantErr: true},
		{name: "no display", env: map[string]string{"WAYLAND_DISPLAY": ""}, want: "none"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{"WAYLAND_DISPLAY": "wayland-0", "XDG_RUNTIME_DIR": directory}
			for key, value := range tc.env {
				env[key] = value
			}
			runner := &desktopRunner{endpoint: tc.endpoint, operatingSystem: tc.operatingSystem}
			resolver := Resolver{Runner: runner, Environ: func(key string) string { return env[key] }, UID: os.Geteuid()}
			selection, err := resolver.Discover(context.Background(), config.GUIIntent{Specified: tc.explicit, Mode: "auto"}, identity.Workspace{}, compose.AssetPaths{})
			if (err != nil) != tc.wantErr || !tc.wantErr && selection.Mode != tc.want {
				t.Fatalf("selection=%#v err=%v", selection, err)
			}
			if runner.runs != 0 {
				t.Fatal("discovery mutated credentials")
			}
			if tc.contextName != "" {
				want := []string{"docker", "context", "inspect", tc.contextName, "--format", "{{.Endpoints.docker.Host}}"}
				if len(runner.calls) == 0 || !reflect.DeepEqual(runner.calls[0], want) {
					t.Fatalf("context precedence: %#v", runner.calls)
				}
			}
			if env["DOCKER_HOST"] != "" && env["DOCKER_CONTEXT"] == "" && len(runner.calls) != 0 {
				t.Fatalf("remote DOCKER_HOST should be rejected without consulting stored context: %#v", runner.calls)
			}
		})
	}
}

func TestXAuthorityDiscoveryDoesNotReplaceCredentials(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "new-state-root")
	runner := &desktopRunner{}
	resolver := Resolver{Runner: runner, Environ: func(key string) string {
		if key == "XDG_STATE_HOME" {
			return state
		}
		return ""
	}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	stateHome, credential, err := resolver.xAuthority(ctx, ":123")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("discovery created its state directory: %v", err)
	}
	selection := Selection{Mode: "x11", stateHome: stateHome, project: "project", authority: credential}
	if err := resolver.Prepare(ctx, selection); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(state, "hcorral", "gui", "project", "xauthority")
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("credential mode: %v %v", info, err)
	}
	if _, _, err := resolver.xAuthority(ctx, ":123"); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(target)
	if err != nil || !os.SameFile(info, after) || runner.runs != 1 {
		t.Fatal("read-only lookup replaced the active credential")
	}
}
