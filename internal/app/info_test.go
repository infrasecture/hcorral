package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func TestInfoJSONHasStableSchemaAndRedactsComposePrefix(t *testing.T) {
	t.Parallel()
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(workspace)
	cfg.Command = []string{"info", "--format=json"}
	cfg.ComposeCommand = []string{"/opt/policy compose", "--token=must-not-appear", "compose"}
	cfg.Sources = map[string]string{"compose_command": "environment"}
	var stdout, stderr bytes.Buffer
	docker := containerruntime.NewDocker(&fakeRunner{}).WithStreams(&stdout, &stderr)
	code := printSnapshot(context.Background(), Streams{Out: &stdout, Err: &stderr}, cfg, workspace, nil, nil, nil, errors.New("Docker unavailable"), nil, &fakeRunner{}, docker)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "must-not-appear") || strings.Contains(stdout.String(), "/opt/") {
		t.Fatalf("structured information leaked Compose prefix: %s", stdout.String())
	}

	var document map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schema", "launcher", "workspace", "project", "configuration", "ownership", "mycodex", "container", "image", "state", "mounts", "gui", "compose", "session", "update", "docker"} {
		if _, ok := document[key]; !ok {
			t.Errorf("missing schema field %q", key)
		}
	}
	configuration := document["configuration"].(map[string]any)
	commandSummary := configuration["compose_command"].(map[string]any)
	if commandSummary["executable"] != "policy compose" || commandSummary["argument_count"] != float64(2) {
		t.Fatalf("redacted command = %#v", commandSummary)
	}
}

func TestInfoWithDockerPerformsNoMutation(t *testing.T) {
	t.Parallel()
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{workspace: workspace}
	cfg := testConfig(workspace)
	cfg.Command = []string{"info", "--format=json"}
	var stdout, stderr bytes.Buffer
	code := runOperational(cfg, workspace, Streams{In: bytes.NewReader(nil), Out: &stdout, Err: &stderr}, runner)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if len(runner.runs) != 0 || len(runner.replaced) != 0 {
		t.Fatalf("info mutated runtime: runs=%v replace=%v", runner.runs, runner.replaced)
	}
	var got snapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got.Schema != 1 || got.Container.Status != "absent" || !got.Docker.Available {
		t.Fatalf("unexpected snapshot: %#v", got)
	}
}

func TestInfoReportsOwnershipCollisionWithoutExecutingInContainer(t *testing.T) {
	t.Parallel()
	workspacePath := t.TempDir()
	workspace, err := identity.Resolve(workspacePath, workspacePath, "")
	if err != nil {
		t.Fatal(err)
	}
	foreign := containerruntime.Container{ID: "foreign", Name: "/" + workspace.Project}
	foreign.Config.Image = "example.invalid/foreign:latest"
	foreign.Config.Labels = map[string]string{
		identity.LabelWorkspaceID:     strings.Repeat("f", 64),
		identity.LabelWorkspaceScheme: identity.WorkspaceSchemeVersion,
		identity.LabelRuntimeSchema:   identity.RuntimeSchemaVersion,
	}
	foreign.State.Status = "running"
	foreign.State.Running = true
	runner := &fakeRunner{workspace: workspace, containers: []containerruntime.Container{foreign}}
	cfg := testConfig(workspace)
	cfg.Command = []string{"info", "--format=json"}
	var stdout, stderr bytes.Buffer
	code := runOperational(cfg, workspace, Streams{In: bytes.NewReader(nil), Out: &stdout, Err: &stderr}, runner)
	if code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var got snapshot
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Ownership.Status != "collision" || got.Ownership.Error == "" || got.Session.Status != "not-inspected-unowned" {
		t.Fatalf("collision snapshot = %#v", got)
	}
	for _, argv := range runner.captures {
		if len(argv) >= 2 && argv[0] == "docker" && argv[1] == "exec" {
			t.Fatalf("info executed inside unowned container: %#v", argv)
		}
	}
}

var _ command.Runner = (*fakeRunner)(nil)
