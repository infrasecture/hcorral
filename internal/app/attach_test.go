package app

import (
	"bytes"
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

func TestAttachSuppliesNoticesAsDataUsingDeployedSettings(t *testing.T) {
	container := containerruntime.Container{ID: "immutable-container-id"}
	container.Config.Env = []string{"HCORRAL_HOST_UID=12345", "HCORRAL_CONTAINER_HOME=/persisted home", "HCORRAL_BYOBU_SESSION=custom-session"}
	container.Config.Labels = map[string]string{identity.LabelGUI: "wayland"}
	cfg := config.Config{ContainerHome: "/different-home", Session: "other"}
	report := "line one\n$(touch /tmp/never-execute) `false` '; #{bad}"
	runner := &fakeRunner{}
	var stderr bytes.Buffer
	if code := replaceAttach(cfg, &container, report, true, Streams{Err: &stderr}, runner); code != 0 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	if !containsSequence(runner.replaced, []string{"docker", "exec", "-it", container.ID, "gosu", "12345", "env", "HOME=/persisted home", "bash", "--noprofile", "--norc", "-c", tmuxNotices}) {
		t.Fatalf("attach does not use deployed settings and launcher helper: %#v", runner.replaced)
	}
	tail := runner.replaced[len(runner.replaced)-6:]
	want := []string{"hcorral-tmux", "custom-session", "wayland", "hcorral: GUI access enabled (Wayland); the container can access the selected desktop socket.", report, "1"}
	if !reflect.DeepEqual(tail, want) {
		t.Fatalf("argument boundaries lost: %#v", tail)
	}
	if strings.Contains(tmuxNotices, report) {
		t.Fatal("report was interpolated into executable shell code")
	}
}

func TestNoticesReopensWithoutRecoveryOrUpdateProbe(t *testing.T) {
	workspace, err := identity.Resolve(t.TempDir(), "", "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	container := containerruntime.Container{ID: "owned", Name: "/" + workspace.Project}
	container.Config.Labels = ownedLabels(workspace, "none")
	container.State.Running, container.State.Status = true, "running"
	cfg := testConfig(workspace)
	cfg.Command, cfg.UpdateCheck = []string{"notices"}, true
	for _, missing := range []bool{false, true} {
		runner := &fakeRunner{containers: []containerruntime.Container{container}, workspace: workspace, sessionMissing: missing}
		var out, stderr bytes.Buffer
		code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, runner)
		if missing {
			if code == 0 || len(runner.replaced) != 0 {
				t.Fatal("missing session should not be created by notices")
			}
		} else if code != 0 || runner.replaced[len(runner.replaced)-1] != "1" {
			t.Fatalf("notices did not reopen: %d %s", code, &stderr)
		}
		for _, argv := range runner.captures {
			joined := strings.Join(argv, " ")
			if strings.Contains(joined, "--version") || strings.Contains(joined, "session-init") {
				t.Fatalf("notices triggered a probe/recovery: %q", argv)
			}
		}
		if len(runner.runs) != 0 {
			t.Fatalf("unexpected lifecycle mutation: %#v", runner.runs)
		}
	}
}

func TestReadinessUsesDeployedSessionAndRuntimeIdentity(t *testing.T) {
	container := containerruntime.Container{ID: "id"}
	container.State.Running = true
	container.Config.Env = []string{"HCORRAL_HOST_UID=501", "HCORRAL_CONTAINER_HOME=/home/actual", "HCORRAL_BYOBU_SESSION=actual"}
	runner := &fakeRunner{}
	if !sessionReady(context.Background(), containerruntime.NewDocker(runner), config.Config{Session: "wrong"}, &container) {
		t.Fatal("session probe failed")
	}
	want := []string{"docker", "exec", "id", "gosu", "501", "env", "HOME=/home/actual", "byobu-tmux", "has-session", "-t", "=actual"}
	if !reflect.DeepEqual(runner.captures[0], want) {
		t.Fatalf("probe = %#v, want %#v", runner.captures[0], want)
	}
}
