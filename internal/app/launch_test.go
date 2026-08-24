package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type fakeRunner struct {
	containers     []containerruntime.Container
	volumes        map[string]containerruntime.Volume
	captures       [][]string
	replaced       []string
	runs           [][]string
	workspace      identity.Workspace
	sessionMissing bool
}

func (f *fakeRunner) Capture(_ context.Context, argv, env []string) (command.Result, error) {
	f.captures = append(f.captures, append([]string(nil), argv...))
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
	case len(argv) >= 4 && argv[0] == "docker" && argv[1] == "network" && argv[2] == "inspect":
		return command.Result{Stderr: []byte("No such network")}, errors.New("exit status 1")
	case len(argv) >= 4 && argv[0] == "docker" && argv[1] == "volume" && argv[2] == "inspect":
		volume, ok := f.volumes[argv[3]]
		if !ok {
			return command.Result{Stderr: []byte("No such volume")}, errors.New("exit status 1")
		}
		content, _ := json.Marshal([]containerruntime.Volume{volume})
		return command.Result{Stdout: content}, nil
	case strings.Contains(joined, "config\x00--format\x00json"):
		value := func(key string) string {
			for _, entry := range env {
				if strings.HasPrefix(entry, key+"=") {
					return strings.TrimPrefix(entry, key+"=")
				}
			}
			return ""
		}
		environment := map[string]string{
			"HCORRAL_LAUNCHED_BY_WRAPPER": "1",
			"HCORRAL_HOST_UID":            value("HCORRAL_HOST_UID"), "HCORRAL_HOST_GID": value("HCORRAL_HOST_GID"),
			"HCORRAL_HOST_USER": value("HCORRAL_HOST_USER"), "HCORRAL_HOST_GROUP": value("HCORRAL_HOST_GROUP"),
			"HCORRAL_HOST_GROUPS": value("HCORRAL_HOST_GROUPS"), "HCORRAL_CONTAINER_HOME": mustHome(),
			"HCORRAL_WORKDIR": f.workspace.Path, "CODEX_HOME": mustHome() + "/.codex",
			"HCORRAL_BYOBU_SESSION": "hcorral", "HCORRAL_AUTO_ATTACH": "false", "HCORRAL_ATTACH_HINT": "hcorral",
		}
		document := map[string]any{"services": map[string]any{"hcorral": map[string]any{"image": "ghcr.io/infrasecture/hcorral:latest", "container_name": f.workspace.Project, "working_dir": f.workspace.Path, "restart": "unless-stopped", "init": true, "tty": true, "stdin_open": true, "labels": map[string]string{identity.LabelWorkspaceID: f.workspace.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelRuntimeSchema: "1", identity.LabelGUI: "none"}, "environment": environment, "volumes": []map[string]any{{"type": "bind", "source": f.workspace.Path, "target": f.workspace.Path}, {"type": "volume", "source": "hcorral_state", "target": mustHome()}}}}, "volumes": map[string]any{"hcorral_state": map[string]any{"name": f.workspace.Project, "external": true}}}
		content, _ := json.Marshal(document)
		return command.Result{Stdout: content}, nil
	case strings.Contains(joined, "config\x00--hash\x00*"):
		return command.Result{Stdout: []byte("hcorral hash\n")}, nil
	case strings.Contains(joined, "byobu-tmux\x00has-session"):
		if f.sessionMissing {
			return command.Result{}, errors.New("exit status 1")
		}
		return command.Result{}, nil
	case strings.Contains(joined, "/usr/local/bin/hcorral-session-init"):
		f.sessionMissing = false
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

func TestMissingSessionRecoveryReinspectsUnderLock(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	container := containerruntime.Container{ID: "owned", Name: "/" + workspace.Project, Mounts: []containerruntime.Mount{{Type: "bind", Source: workspace.Path, Destination: workspace.Path}, {Type: "volume", Name: "hcorral_state", Destination: mustHome()}}}
	container.Config.Image = "ghcr.io/infrasecture/hcorral:latest"
	container.Config.Labels = map[string]string{identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelRuntimeSchema: "1", identity.LabelGUI: "none", "com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral", "com.docker.compose.config-hash": "hash"}
	container.State.Status, container.State.Running = "running", true
	runner := &fakeRunner{containers: []containerruntime.Container{container}, workspace: workspace, sessionMissing: true}
	var out, stderr bytes.Buffer
	if code := runOperational(testConfig(workspace), workspace, Streams{In: bytes.NewReader(nil), Out: &out, Err: &stderr}, runner); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	listCalls, recoveryCalls := 0, 0
	for _, argv := range runner.captures {
		joined := strings.Join(argv, "\x00")
		if len(argv) >= 2 && argv[0] == "docker" && argv[1] == "ps" {
			listCalls++
		}
		if strings.Contains(joined, "/usr/local/bin/hcorral-session-init") {
			recoveryCalls++
		}
	}
	if listCalls < 2 || recoveryCalls != 1 {
		t.Fatalf("list calls=%d recovery calls=%d captures=%#v", listCalls, recoveryCalls, runner.captures)
	}
	if len(runner.replaced) == 0 {
		t.Fatal("recovered session was not attached")
	}
}

func TestDirectStartRefusesMismatchedManagedState(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	container := containerruntime.Container{ID: "owned", Name: "/" + workspace.Project, Mounts: []containerruntime.Mount{{Type: "bind", Source: workspace.Path, Destination: workspace.Path}, {Type: "volume", Name: "hcorral_state", Destination: mustHome()}}}
	container.Config.Labels = map[string]string{identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelRuntimeSchema: "1", identity.LabelGUI: "none", "com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral", "com.docker.compose.config-hash": "hash"}
	container.State.Status = "exited"
	foreign := containerruntime.Volume{Name: "hcorral_state", Labels: map[string]string{identity.LabelStateKind: "foreign"}}
	runner := &fakeRunner{containers: []containerruntime.Container{container}, volumes: map[string]containerruntime.Volume{"hcorral_state": foreign}, workspace: workspace}
	cfg := testConfig(workspace)
	cfg.Command = []string{"start"}
	var out, stderr bytes.Buffer
	if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, runner); code != 1 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if len(runner.runs) != 0 {
		t.Fatalf("start mutated a mismatched state selection: %#v", runner.runs)
	}
}

func TestDarwinNeverOperatesOnGUIContainer(t *testing.T) {
	t.Parallel()
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	container := containerruntime.Container{ID: "owned", Name: "/" + workspace.Project}
	container.Config.Labels = map[string]string{identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: identity.WorkspaceSchemeVersion, identity.LabelRuntimeSchema: identity.RuntimeSchemaVersion, identity.LabelGUI: "x11", "com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral", "com.docker.compose.config-hash": "hash"}
	container.State.Status, container.State.Running = "exited", false
	runner := &fakeRunner{containers: []containerruntime.Container{container}, workspace: workspace}
	cfg := testConfig(workspace)
	cfg.Platform = "darwin"
	cfg.Command = []string{"start"}
	var out, stderr bytes.Buffer
	if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, runner); code != 2 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if len(runner.runs) != 0 || len(runner.replaced) != 0 {
		t.Fatalf("Darwin GUI refusal mutated runtime: %#v %#v", runner.runs, runner.replaced)
	}
}

func TestSanitizedLogLinesAreBoundedAndRedacted(t *testing.T) {
	t.Parallel()
	lines := make([]string, 0, 85)
	for index := 0; index < 85; index++ {
		lines = append(lines, fmt.Sprintf("line-%d", index))
	}
	lines[84] = "\x1b[31mfailed\x1b[0m token=abc password=hunter2\x00"
	got := sanitizedLogLines([]byte(strings.Join(lines, "\n")+"\n"), 80)
	if len(got) != 80 || got[0] != "line-5" {
		t.Fatalf("bounded lines = %d first=%q", len(got), got[0])
	}
	last := got[len(got)-1]
	if last != "failed token=<redacted> password=<redacted>" {
		t.Fatalf("sanitized last line = %q", last)
	}
}

func TestComposeMutationClassificationFailsSafe(t *testing.T) {
	t.Parallel()
	for _, command := range []string{"up", "down", "rm", "kill", "run", "pull", "unknown-future-command"} {
		if !composeCommandMutates(command) {
			t.Errorf("%q was not classified as mutating", command)
		}
	}
	for _, command := range []string{"ps", "config", "images", "logs", "top", "events"} {
		if composeCommandMutates(command) {
			t.Errorf("%q was classified as mutating", command)
		}
	}
}

func TestVerifyProjectContainersRejectsResidueAndConflictingSidecars(t *testing.T) {
	t.Parallel()
	workspace := identity.Workspace{Project: "hcorral-demo-aaaaaaa", FullID: strings.Repeat("a", 64)}
	primary := containerruntime.Container{ID: "primary", Name: "/" + workspace.Project}
	primary.Config.Labels = map[string]string{
		"com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral", "com.docker.compose.config-hash": "primary-hash",
	}
	sidecar := containerruntime.Container{ID: "sidecar", Name: "/" + workspace.Project + "-sidecar-1"}
	sidecar.Config.Labels = map[string]string{
		"com.docker.compose.project": workspace.Project, "com.docker.compose.service": "sidecar", "com.docker.compose.config-hash": "sidecar-hash",
	}
	if err := verifyProjectContainers([]containerruntime.Container{sidecar}, workspace, nil); err == nil {
		t.Fatal("sidecar residue without the primary container was accepted")
	}
	if err := verifyProjectContainers([]containerruntime.Container{primary, sidecar}, workspace, &primary); err != nil {
		t.Fatalf("valid project membership was rejected: %v", err)
	}
	sidecar.Config.Labels[identity.LabelWorkspaceID] = strings.Repeat("b", 64)
	if err := verifyProjectContainers([]containerruntime.Container{primary, sidecar}, workspace, &primary); err == nil {
		t.Fatal("sidecar with conflicting hcorral workspace identity was accepted")
	}
}

func TestChildExitCodePreservesComposeStatus(t *testing.T) {
	t.Parallel()
	err := exec.Command("sh", "-c", "exit 7").Run()
	if got := childExitCode(fmt.Errorf("wrapped: %w", err)); got != 7 {
		t.Fatalf("child exit code = %d", got)
	}
	if got := childExitCode(errors.New("not a child status")); got != 1 {
		t.Fatalf("generic exit code = %d", got)
	}
}

func TestExecTargetsRuntimeUIDAndPreservesArguments(t *testing.T) {
	t.Parallel()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	cfg := config.Config{ContainerHome: "/home/runtime", Workdir: "/work/path with spaces"}
	args := []string{"printf", "%s\\n", "space arg", "line\nbreak"}
	if got := replaceExec(cfg, "hcorral-demo-1234567", args, Streams{}, runner); got != 0 {
		t.Fatalf("replaceExec status = %d", got)
	}
	if len(runner.replaced) == 0 {
		t.Fatal("no replacement argv recorded")
	}
	if !containsSequence(runner.replaced, []string{"gosu", current.Uid, "env", "HOME=/home/runtime", "CODEX_HOME=/home/runtime/.codex"}) {
		t.Fatalf("runtime UID/environment missing from argv: %#v", runner.replaced)
	}
	wantTail := append([]string{"bash", "/work/path with spaces"}, args...)
	if len(runner.replaced) < len(wantTail) {
		t.Fatalf("replacement argv too short: %#v", runner.replaced)
	}
	if got := runner.replaced[len(runner.replaced)-len(wantTail):]; !reflect.DeepEqual(got, wantTail) {
		t.Fatalf("replacement tail = %#v, want %#v", got, wantTail)
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

func containsSequence(values, want []string) bool {
	for index := 0; index+len(want) <= len(values); index++ {
		if reflect.DeepEqual(values[index:index+len(want)], want) {
			return true
		}
	}
	return false
}
