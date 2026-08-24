package compose

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type failingCaptureRunner struct{}

func (failingCaptureRunner) Capture(context.Context, []string, []string) (command.Result, error) {
	return command.Result{Stderr: []byte("rendered-password=must-not-appear")}, errors.New("exit status 1")
}

func (failingCaptureRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}

func (failingCaptureRunner) Replace([]string, []string) error {
	return errors.New("unexpected Replace")
}

func TestCaptureErrorDoesNotPromoteRenderedStderr(t *testing.T) {
	t.Parallel()
	project := Project{
		Runner:     failingCaptureRunner{},
		Invocation: Invocation{Prefix: []string{"docker", "compose"}, Project: "demo", Directory: "/work"},
		Out:        &bytes.Buffer{},
		Err:        &bytes.Buffer{},
	}
	result, err := project.Capture(context.Background(), "config", "--format", "json")
	if err == nil {
		t.Fatal("capture unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), "must-not-appear") {
		t.Fatalf("captured stderr leaked through error: %v", err)
	}
	if !bytes.Contains(result.Stderr, []byte("must-not-appear")) {
		t.Fatal("caller lost captured diagnostics")
	}
}

var _ command.Runner = failingCaptureRunner{}

type renderedRunner struct{ document map[string]any }

func (r renderedRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	if strings.Contains(strings.Join(argv, "\x00"), "config\x00--format\x00json") {
		content, err := json.Marshal(r.document)
		return command.Result{Stdout: content}, err
	}
	return command.Result{}, nil
}
func (renderedRunner) Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error {
	return errors.New("unexpected Run")
}
func (renderedRunner) Replace([]string, []string) error { return errors.New("unexpected Replace") }

func TestRenderAndValidateProtectsEveryManagedField(t *testing.T) {
	t.Parallel()
	workspace := identity.Workspace{Path: "/work/demo", FullID: strings.Repeat("a", 64), Project: "hcorral-demo-aaaaaaa"}
	cfg := config.Config{Workspace: workspace.Path, ImageName: "example/hcorral", ImageTag: "v1", ContainerHome: "/home/alice", Workdir: workspace.Path, Session: "corral"}
	env := []string{
		"HCORRAL_HOST_UID=1000", "HCORRAL_HOST_GID=1000", "HCORRAL_HOST_USER=alice", "HCORRAL_HOST_GROUP=users",
		"HCORRAL_HOST_GROUPS=1000:users", "HCORRAL_AUTO_ATTACH=true",
	}
	base := func() map[string]any {
		return map[string]any{
			"services": map[string]any{"hcorral": map[string]any{
				"image": "example/hcorral:v1", "container_name": workspace.Project, "working_dir": workspace.Path,
				"restart": "unless-stopped", "init": true, "tty": true, "stdin_open": true,
				"labels": map[string]string{identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: identity.WorkspaceSchemeVersion, identity.LabelRuntimeSchema: identity.RuntimeSchemaVersion, identity.LabelGUI: "none"},
				"environment": map[string]string{
					"HCORRAL_LAUNCHED_BY_WRAPPER": "1", "HCORRAL_HOST_UID": "1000", "HCORRAL_HOST_GID": "1000",
					"HCORRAL_HOST_USER": "alice", "HCORRAL_HOST_GROUP": "users", "HCORRAL_HOST_GROUPS": "1000:users",
					"HCORRAL_CONTAINER_HOME": "/home/alice", "HCORRAL_WORKDIR": workspace.Path, "CODEX_HOME": "/home/alice/.codex",
					"HCORRAL_BYOBU_SESSION": "corral", "HCORRAL_AUTO_ATTACH": "true", "HCORRAL_ATTACH_HINT": "hcorral",
				},
				"volumes": []map[string]any{{"type": "bind", "source": workspace.Path, "target": workspace.Path}, {"type": "volume", "source": "hcorral_state", "target": "/home/alice"}},
			}},
			"volumes": map[string]any{"hcorral_state": map[string]any{"name": "hcorral_state", "external": true}},
		}
	}
	validate := func(document map[string]any) error {
		project := Project{Runner: renderedRunner{document: document}, Invocation: Invocation{Prefix: []string{"docker", "compose"}, Env: env}}
		_, err := project.RenderAndValidate(context.Background(), cfg, workspace, "none", "hcorral_state")
		return err
	}
	if err := validate(base()); err != nil {
		t.Fatalf("valid base rejected: %v", err)
	}
	safeExtensions := base()
	serviceMap(safeExtensions)["configs"] = []map[string]any{{"source": "editor", "target": "/etc/editor.conf"}}
	serviceMap(safeExtensions)["secrets"] = []map[string]any{{"source": "token", "target": "/run/secrets/token"}}
	serviceMap(safeExtensions)["tmpfs"] = []string{"/tmp/overlay-cache:mode=700"}
	if err := validate(safeExtensions); err != nil {
		t.Fatalf("safe config, secret, and tmpfs extensions were rejected: %v", err)
	}
	mutations := map[string]func(map[string]any){
		"lifecycle":  func(document map[string]any) { serviceMap(document)["init"] = false },
		"entrypoint": func(document map[string]any) { serviceMap(document)["entrypoint"] = []string{"sh"} },
		"reserved label": func(document map[string]any) {
			serviceMap(document)["labels"].(map[string]string)["ai.infrasecture.hcorral.extra"] = "bad"
		},
		"host identity": func(document map[string]any) {
			serviceMap(document)["environment"].(map[string]string)["HCORRAL_HOST_UID"] = "0"
		},
		"reserved environment": func(document map[string]any) {
			serviceMap(document)["environment"].(map[string]string)["HCORRAL_UNMANAGED"] = "bad"
		},
		"read-only workspace": func(document map[string]any) {
			serviceMap(document)["volumes"].([]map[string]any)[0]["read_only"] = true
		},
		"headless display": func(document map[string]any) {
			serviceMap(document)["environment"].(map[string]string)["DISPLAY"] = ":0"
		},
		"runtime path mount": func(document map[string]any) {
			service := serviceMap(document)
			service["volumes"] = append(service["volumes"].([]map[string]any), map[string]any{"type": "bind", "source": "/tmp/attack", "target": "/etc/hcorral"})
		},
		"privileged":   func(document map[string]any) { serviceMap(document)["privileged"] = true },
		"host network": func(document map[string]any) { serviceMap(document)["network_mode"] = "host" },
		"host pid":     func(document map[string]any) { serviceMap(document)["pid"] = "host" },
		"host ipc":     func(document map[string]any) { serviceMap(document)["ipc"] = "host" },
		"host userns":  func(document map[string]any) { serviceMap(document)["userns_mode"] = "host" },
		"device": func(document map[string]any) {
			serviceMap(document)["devices"] = []map[string]any{{"source": "/dev/dri", "target": "/dev/dri", "permissions": "rwm"}}
		},
		"capability": func(document map[string]any) { serviceMap(document)["cap_add"] = []string{"SYS_ADMIN"} },
		"security option": func(document map[string]any) {
			serviceMap(document)["security_opt"] = []string{"seccomp=unconfined"}
		},
		"read-only root": func(document map[string]any) { serviceMap(document)["read_only"] = true },
		"runtime user":   func(document map[string]any) { serviceMap(document)["user"] = "1000" },
		"host UTS":       func(document map[string]any) { serviceMap(document)["uts"] = "host" },
		"host cgroup":    func(document map[string]any) { serviceMap(document)["cgroup"] = "host" },
		"custom runtime": func(document map[string]any) { serviceMap(document)["runtime"] = "nvidia" },
		"supplementary group": func(document map[string]any) {
			serviceMap(document)["group_add"] = []string{"docker"}
		},
		"volumes from": func(document map[string]any) { serviceMap(document)["volumes_from"] = []string{"foreign"} },
		"GPU request":  func(document map[string]any) { serviceMap(document)["gpus"] = "all" },
		"API socket":   func(document map[string]any) { serviceMap(document)["use_api_socket"] = true },
		"managed config": func(document map[string]any) {
			serviceMap(document)["configs"] = []map[string]any{{"source": "attack", "target": "/usr/local/bin/entrypoint.sh"}}
		},
		"default config target": func(document map[string]any) {
			serviceMap(document)["configs"] = []map[string]any{{"source": "workspace"}}
		},
		"managed secret": func(document map[string]any) {
			serviceMap(document)["secrets"] = []map[string]any{{"source": "attack", "target": "/etc/hcorral/attack"}}
		},
		"managed tmpfs": func(document map[string]any) { serviceMap(document)["tmpfs"] = []string{"/home/alice:mode=700"} },
		"Docker socket source": func(document map[string]any) {
			service := serviceMap(document)
			service["volumes"] = append(service["volumes"].([]map[string]any), map[string]any{"type": "bind", "source": "/var/run/docker.sock", "target": "/tmp/docker.sock"})
		},
		"broad X11 source": func(document map[string]any) {
			service := serviceMap(document)
			service["volumes"] = append(service["volumes"].([]map[string]any), map[string]any{"type": "bind", "source": "/tmp/.X11-unix", "target": "/mnt/x11"})
		},
		"D-Bus source": func(document map[string]any) {
			service := serviceMap(document)
			service["volumes"] = append(service["volumes"].([]map[string]any), map[string]any{"type": "bind", "source": "/run/dbus", "target": "/mnt/dbus"})
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			document := base()
			mutate(document)
			if err := validate(document); err == nil {
				t.Fatal("managed-field mutation was accepted")
			}
		})
	}
}

func TestValidateGUIRequiresExactSocketAndCredentialMounts(t *testing.T) {
	t.Parallel()
	project := Project{Invocation: Invocation{Env: []string{"HCORRAL_X11_DISPLAY=:2", "HCORRAL_X11_SOCKET=/tmp/.X11-unix/X2", "HCORRAL_X11_AUTHORITY=/state/xauth", "HCORRAL_WAYLAND_SOCKET=/run/user/1000/wayland-0"}}}
	x11 := RenderedService{
		Environment: map[string]string{"DISPLAY": ":2", "XAUTHORITY": "/tmp/.hcorral-xauthority"},
		Volumes: []RenderedVolume{
			{Type: "bind", Source: "/tmp/.X11-unix/X2", Target: "/tmp/.X11-unix/X2", ReadOnly: true},
			{Type: "bind", Source: "/state/xauth", Target: "/tmp/.hcorral-xauthority", ReadOnly: true},
		},
	}
	if err := project.validateGUI(x11, "x11"); err != nil {
		t.Fatal(err)
	}
	x11.Volumes[0].Source = "/tmp/.X11-unix/X9"
	if err := project.validateGUI(x11, "x11"); err == nil {
		t.Fatal("wrong X11 socket was accepted")
	}
	wayland := RenderedService{Environment: map[string]string{"WAYLAND_DISPLAY": "/tmp/.hcorral-wayland", "XDG_SESSION_TYPE": "wayland"}, Volumes: []RenderedVolume{{Type: "bind", Source: "/run/user/1000/wayland-0", Target: "/tmp/.hcorral-wayland", ReadOnly: true}}}
	if err := project.validateGUI(wayland, "wayland"); err != nil {
		t.Fatal(err)
	}
	wayland.Volumes[0].ReadOnly = false
	if err := project.validateGUI(wayland, "wayland"); err == nil {
		t.Fatal("writable Wayland socket was accepted")
	}
}

func serviceMap(document map[string]any) map[string]any {
	return document["services"].(map[string]any)["hcorral"].(map[string]any)
}

var _ command.Runner = renderedRunner{}
