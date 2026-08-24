package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type fakeRunner struct {
	containers []containerruntime.Container
	replaced   []string
	runs       [][]string
	workspace  identity.Workspace
}

func (f *fakeRunner) Capture(_ context.Context, argv, _ []string) (command.Result, error) {
	joined := strings.Join(argv, "\x00")
	switch {
	case len(argv) >= 3 && argv[0] == "docker" && argv[1] == "ps":
		ids := []string{}
		for _, container := range f.containers {
			ids = append(ids, container.ID)
		}
		return command.Result{Stdout: []byte(strings.Join(ids, "\n"))}, nil
	case len(argv) >= 3 && argv[0] == "docker" && argv[1] == "inspect":
		content, _ := json.Marshal(f.containers)
		return command.Result{Stdout: content}, nil
	case strings.Contains(joined, "config\x00--format\x00json"):
		document := map[string]any{"services": map[string]any{"hcorral": map[string]any{"image": "ghcr.io/infrasecture/hcorral:latest", "container_name": f.workspace.Project, "working_dir": f.workspace.Path, "labels": map[string]string{identity.LabelWorkspaceID: f.workspace.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelRuntimeSchema: "1", identity.LabelGUI: "none"}, "environment": map[string]string{"HCORRAL_LAUNCHED_BY_WRAPPER": "1"}, "volumes": []map[string]any{{"type": "bind", "source": f.workspace.Path, "target": f.workspace.Path}, {"type": "volume", "source": "hcorral_state", "target": mustHome()}}}}, "volumes": map[string]any{"hcorral_state": map[string]any{"name": f.workspace.Project, "external": true}}}
		content, _ := json.Marshal(document)
		return command.Result{Stdout: content}, nil
	case strings.Contains(joined, "config\x00--hash\x00*"):
		return command.Result{Stdout: []byte("hcorral hash\n")}, nil
	case strings.Contains(joined, "byobu-tmux\x00has-session"):
		return command.Result{}, nil
	default:
		return command.Result{}, nil
	}
}
func (f *fakeRunner) Run(_ context.Context, argv, _ []string, _ io.Reader, _, _ io.Writer) error {
	f.runs = append(f.runs, append([]string(nil), argv...))
	return nil
}
func (f *fakeRunner) Replace(argv, _ []string) error {
	f.replaced = append([]string(nil), argv...)
	return nil
}

func TestLegacyConflictHasZeroMutation(t *testing.T) {
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	legacy := containerruntime.Container{ID: "legacy", Name: "/demo-codex", Mounts: []containerruntime.Mount{{Type: "bind", Source: workspace.Path, Destination: workspace.Path}}}
	legacy.Config.Labels = map[string]string{"io.infrasecture.mycodex.gui": "none"}
	legacy.State.Status = "running"
	legacy.State.Running = true
	runner := &fakeRunner{containers: []containerruntime.Container{legacy}, workspace: workspace}
	var out, stderr bytes.Buffer
	cfg := testConfig(workspace)
	code := runOperational(cfg, workspace, Streams{In: bytes.NewReader(nil), Out: &out, Err: &stderr}, runner)
	if code != 3 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if len(runner.runs) != 0 || len(runner.replaced) != 0 {
		t.Fatalf("mutation occurred: runs=%v replace=%v", runner.runs, runner.replaced)
	}
}

func TestRunningContainerAttachesWithoutMutation(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	container := containerruntime.Container{ID: "owned", Name: "/" + workspace.Project, Mounts: []containerruntime.Mount{{Type: "bind", Source: workspace.Path, Destination: workspace.Path}, {Type: "volume", Name: workspace.Project, Destination: mustHome()}}}
	container.Config.Image = "ghcr.io/infrasecture/hcorral:latest"
	container.Config.Labels = map[string]string{identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelRuntimeSchema: "1", identity.LabelGUI: "none", "com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral", "com.docker.compose.config-hash": "hash"}
	container.State.Status = "running"
	container.State.Running = true
	runner := &fakeRunner{containers: []containerruntime.Container{container}, workspace: workspace}
	var out, stderr bytes.Buffer
	cfg := testConfig(workspace)
	code := runOperational(cfg, workspace, Streams{In: bytes.NewReader(nil), Out: &out, Err: &stderr}, runner)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if len(runner.runs) != 0 {
		t.Fatalf("unexpected mutation: %v", runner.runs)
	}
	if len(runner.replaced) == 0 || runner.replaced[0] != "docker" || !contains(runner.replaced, workspace.Project) {
		t.Fatalf("replace argv=%#v", runner.replaced)
	}
}

func testConfig(workspace identity.Workspace) config.Config {
	return config.Config{CallerDir: workspace.Path, Workspace: workspace.Path, ImageName: "ghcr.io/infrasecture/hcorral", ImageTag: "latest", StateMode: config.StateShared, ComposeCommand: []string{"docker", "compose"}, ContainerHome: mustHome(), Workdir: workspace.Path, WaitTimeoutSeconds: 1, ProgressIntervalSecond: 1, Session: "hcorral", UpdateCheck: false, Platform: "linux"}
}
func mustHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		panic(err)
	}
	return filepath.Clean(home)
}
func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
