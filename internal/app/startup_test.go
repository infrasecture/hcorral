package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type startupRunner struct {
	*fakeRunner
	reference   string
	images      map[string]string
	hash        string
	renderErr   error
	pullErr     error
	pulledID    string
	onPull      func()
	composeEnvs [][]string
}

func (r *startupRunner) Capture(ctx context.Context, argv, env []string) (command.Result, error) {
	joined := strings.Join(argv, "\x00")
	if len(argv) >= 4 && argv[0] == "docker" && argv[1] == "image" && argv[2] == "inspect" {
		r.captures = append(r.captures, argv)
		id := r.images[argv[3]]
		if id == "" {
			return command.Result{Stderr: []byte("No such image")}, errors.New("missing image")
		}
		content, _ := json.Marshal([]containerruntime.Image{{ID: id}})
		return command.Result{Stdout: content}, nil
	}
	if len(argv) >= 5 && argv[0] == "docker" && argv[1] == "inspect" {
		r.captures = append(r.captures, argv)
		var selected []containerruntime.Container
		for _, c := range r.containers {
			if contains(argv[4:], c.ID) || contains(argv[4:], c.CleanName()) {
				selected = append(selected, c)
			}
		}
		if len(selected) == 0 {
			return command.Result{Stderr: []byte("No such container")}, errors.New("missing container")
		}
		content, _ := json.Marshal(selected)
		return command.Result{Stdout: content}, nil
	}
	if strings.Contains(joined, "config\x00--format\x00json") {
		r.composeEnvs = append(r.composeEnvs, env)
		if r.renderErr != nil {
			return command.Result{}, r.renderErr
		}
		result, err := r.fakeRunner.Capture(ctx, argv, env)
		if err != nil {
			return result, err
		}
		var doc map[string]any
		if err := json.Unmarshal(result.Stdout, &doc); err != nil {
			return result, err
		}
		doc["services"].(map[string]any)["hcorral"].(map[string]any)["image"] = r.reference
		result.Stdout, _ = json.Marshal(doc)
		return result, nil
	}
	if strings.Contains(joined, "config\x00--hash\x00*") {
		r.captures = append(r.captures, argv)
		if r.hash == "" {
			return command.Result{}, errors.New("hash capability unavailable")
		}
		return command.Result{Stdout: []byte("hcorral " + r.hash + "\n")}, nil
	}
	return r.fakeRunner.Capture(ctx, argv, env)
}

func (r *startupRunner) Run(ctx context.Context, argv, env []string, in io.Reader, out, stderr io.Writer) error {
	if err := r.fakeRunner.Run(ctx, argv, env, in, out, stderr); err != nil {
		return err
	}
	if len(argv) > 2 && argv[0] == "docker" && argv[1] == "pull" {
		if r.onPull != nil {
			r.onPull()
		}
		if r.pullErr != nil {
			return r.pullErr
		}
		r.images[argv[2]] = r.pulledID
	}
	if len(argv) > 2 && argv[0] == "docker" && argv[1] == "start" {
		for i := range r.containers {
			if r.containers[i].ID == argv[2] || r.containers[i].CleanName() == argv[2] {
				r.containers[i].State.Running, r.containers[i].State.Status = true, "running"
			}
		}
	}
	if containsSequence(argv, []string{"up", "-d"}) {
		container := startupContainer(r.workspace)
		container.ID = "replacement"
		container.ImageID = r.images[r.reference]
		container.Config.Image = r.reference
		container.State.Running, container.State.Status = true, "running"
		r.containers = []containerruntime.Container{container}
	}
	return nil
}

func startupContainer(workspace identity.Workspace) containerruntime.Container {
	container := containerruntime.Container{ID: "original", ImageID: "sha256:original", Name: "/" + workspace.Project}
	container.Config.Labels = ownedLabels(workspace, "none")
	container.Config.Labels["com.docker.compose.config-hash"] = strings.Repeat("a", 64)
	container.Config.Image = "registry.test/selected:latest"
	container.Config.Env = []string{"HCORRAL_HOST_UID=12345", "HCORRAL_CONTAINER_HOME=" + mustHome(), "HCORRAL_BYOBU_SESSION=hcorral"}
	container.State.Status = "exited"
	container.Mounts = []containerruntime.Mount{{Type: "bind", Source: workspace.Path, Destination: workspace.Path}, {Type: "volume", Name: "hcorral_state", Destination: mustHome()}}
	return container
}

func startupFixture(t *testing.T) (config.Config, identity.Workspace, *startupRunner) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	workspace, err := identity.Resolve(t.TempDir(), "", "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(workspace)
	cfg.AutoPull = true
	runner := &startupRunner{
		fakeRunner: &fakeRunner{workspace: workspace, containers: []containerruntime.Container{startupContainer(workspace)}, volumes: map[string]containerruntime.Volume{"hcorral_state": {Name: "hcorral_state", Labels: identity.SharedVolumeLabels()}}},
		reference:  "registry.test/selected:latest", images: map[string]string{"registry.test/selected:latest": "sha256:original"}, hash: strings.Repeat("a", 64), pulledID: "sha256:updated",
	}
	return cfg, workspace, runner
}

func TestInactiveStartupPolicy(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		configure              func(*config.Config, *startupRunner)
		pulls, starts, creates int
		failure                bool
		notice                 string
	}{
		{name: "changed image and same configuration", pulls: 1, creates: 1, notice: "applying the refreshed image"},
		{name: "unchanged image", configure: func(_ *config.Config, r *startupRunner) { r.pulledID = "sha256:original" }, pulls: 1, starts: 1},
		{name: "offline preserves original despite moved alias", configure: func(_ *config.Config, r *startupRunner) {
			r.pullErr = errors.New("offline")
			r.images[r.reference] = "sha256:other"
		}, pulls: 1, starts: 1, notice: "original image and mounts"},
		{name: "different hash preserves original", configure: func(_ *config.Config, r *startupRunner) { r.hash = strings.Repeat("b", 64) }, pulls: 1, starts: 1, notice: "could not be reproduced"},
		{name: "missing deployed hash preserves original", configure: func(_ *config.Config, r *startupRunner) {
			delete(r.containers[0].Config.Labels, "com.docker.compose.config-hash")
		}, pulls: 1, starts: 1, notice: "could not be reproduced"},
		{name: "unsupported Compose hash preserves original", configure: func(_ *config.Config, r *startupRunner) { r.hash = "" }, pulls: 1, starts: 1, notice: "could not be reproduced"},
		{name: "missing overlay preserves original", configure: func(_ *config.Config, r *startupRunner) { r.renderErr = errors.New("missing overlay") }, starts: 1, notice: "could not reproduce"},
		{name: "pin preserves existing image", configure: func(_ *config.Config, r *startupRunner) { r.reference = "registry.test/selected:1.2.3" }, starts: 1},
		{name: "disabled automatic refresh", configure: func(c *config.Config, _ *startupRunner) { c.AutoPull = false }, starts: 1},
		{name: "running project never pulls", configure: func(_ *config.Config, r *startupRunner) {
			r.containers[0].State.Running, r.containers[0].State.Status = true, "running"
		}},
		{name: "paused project is not stopped", configure: func(_ *config.Config, r *startupRunner) {
			r.containers[0].State.Running, r.containers[0].State.Paused = true, true
			r.containers[0].State.Status = "paused"
		}, failure: true},
		{name: "restarting project is not stopped", configure: func(_ *config.Config, r *startupRunner) {
			r.containers[0].State.Restarting = true
			r.containers[0].State.Status = "restarting"
		}, failure: true},
		{name: "dead project is not stopped", configure: func(_ *config.Config, r *startupRunner) { r.containers[0].State.Status = "dead" }, failure: true},
		{name: "new project refreshes cached latest", configure: func(_ *config.Config, r *startupRunner) { r.containers = nil }, pulls: 1, creates: 1},
		{name: "new project with omitted tag refreshes", configure: func(_ *config.Config, r *startupRunner) {
			r.containers = nil
			r.reference = "registry.test/selected"
			r.images[r.reference] = "sha256:original"
		}, pulls: 1, creates: 1},
		{name: "new project offline uses cache", configure: func(_ *config.Config, r *startupRunner) { r.containers = nil; r.pullErr = errors.New("offline") }, pulls: 1, creates: 1, notice: "using cached"},
		{name: "new project offline without cache fails", configure: func(_ *config.Config, r *startupRunner) {
			r.containers = nil
			r.images = map[string]string{}
			r.pullErr = errors.New("offline")
		}, pulls: 1, failure: true},
		{name: "new cached pin does not refresh", configure: func(_ *config.Config, r *startupRunner) {
			r.containers = nil
			r.reference = "registry.test/selected:1.2.3"
			r.images[r.reference] = "sha256:pinned"
		}, creates: 1},
		{name: "new missing pin is fetched", configure: func(_ *config.Config, r *startupRunner) {
			r.containers = nil
			r.reference = "registry.test/selected:1.2.3"
		}, pulls: 1, creates: 1},
		{name: "opt out still fetches missing image", configure: func(c *config.Config, r *startupRunner) {
			c.AutoPull = false
			r.containers = nil
			r.images = map[string]string{}
		}, pulls: 1, creates: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, workspace, r := startupFixture(t)
			if tc.configure != nil {
				tc.configure(&cfg, r)
			}
			var out, stderr bytes.Buffer
			code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r)
			if (code != 0) != tc.failure {
				t.Fatalf("code=%d: %s", code, &stderr)
			}
			pulls, starts, creates := 0, 0, 0
			for _, argv := range r.runs {
				if containsSequence(argv, []string{"docker", "pull"}) {
					pulls++
					if argv[2] != r.reference {
						t.Fatalf("pulled configured image instead of rendered overlay: %#v", argv)
					}
				}
				if containsSequence(argv, []string{"docker", "start"}) {
					starts++
					if argv[2] != "original" {
						t.Fatalf("start did not preserve original identity: %#v", argv)
					}
				}
				if containsSequence(argv, []string{"up", "-d"}) {
					creates++
				}
			}
			if pulls != tc.pulls || starts != tc.starts || creates != tc.creates {
				t.Fatalf("actions pulls/start/up=%d/%d/%d, want %d/%d/%d; %#v", pulls, starts, creates, tc.pulls, tc.starts, tc.creates, r.runs)
			}
			if tc.notice != "" {
				if !strings.Contains(stderr.String(), tc.notice) {
					t.Fatalf("missing notice %q: %s", tc.notice, &stderr)
				}
				if code == 0 && !strings.Contains(r.replaced[len(r.replaced)-2], tc.notice) {
					t.Fatalf("notice was not retained in tmux: %#v", r.replaced)
				}
			}
		})
	}
}

func TestStartupRechecksAfterPull(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*startupRunner)
		failure bool
	}{
		{"started during pull", func(r *startupRunner) { r.containers[0].State.Running, r.containers[0].State.Status = true, "running" }, false},
		{"replaced during pull", func(r *startupRunner) { r.containers[0].ID = "other-container" }, true},
		{"removed during pull", func(r *startupRunner) { r.containers = nil }, true},
		{"paused during pull", func(r *startupRunner) {
			r.containers[0].State.Running, r.containers[0].State.Paused = true, true
			r.containers[0].State.Status = "paused"
		}, true},
		{"ownership changed during pull", func(r *startupRunner) { r.containers[0].Config.Labels[identity.LabelCorralID] = "foreign" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, workspace, r := startupFixture(t)
			r.onPull = func() { tc.change(r) }
			var out, stderr bytes.Buffer
			code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r)
			if (code != 0) != tc.failure {
				t.Fatalf("code=%d: %s", code, &stderr)
			}
			if len(r.runs) != 1 || !containsSequence(r.runs[0], []string{"docker", "pull"}) {
				t.Fatalf("mutated container after concurrent change: %#v", r.runs)
			}
		})
	}
}

func TestDefaultPreservesDeployedGUIAgainstEnvironmentChanges(t *testing.T) {
	cfg, workspace, r := startupFixture(t)
	cfg.GUI = config.GUIIntent{Specified: true, Mode: "none"}
	cfg.Sources = map[string]string{"gui": "environment"}
	r.containers[0].Config.Labels[identity.LabelGUI] = "wayland"
	r.containers[0].Mounts = append(r.containers[0].Mounts, containerruntime.Mount{Type: "bind", Source: "/original/wayland-0", Destination: "/tmp/.hcorral-wayland"})
	var out, stderr bytes.Buffer
	if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r); code != 0 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	for _, env := range r.composeEnvs {
		if !contains(env, "HCORRAL_GUI_MODE=wayland") || !contains(env, "HCORRAL_WAYLAND_SOCKET=/original/wayland-0") {
			t.Fatal("default refresh rediscovered or changed deployed GUI")
		}
	}
}

func TestCreationRechecksAbsentProjectAfterPull(t *testing.T) {
	cfg, workspace, r := startupFixture(t)
	r.containers = nil
	r.onPull = func() { r.containers = []containerruntime.Container{startupContainer(workspace)} }
	var out, stderr bytes.Buffer
	if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r); code == 0 {
		t.Fatal("container created by another client was not detected")
	}
	if len(r.runs) != 1 || !containsSequence(r.runs[0], []string{"docker", "pull"}) {
		t.Fatalf("creation race mutated an existing project: %#v", r.runs)
	}
}

func TestExplicitPullUsesRenderedImageWithoutRecreation(t *testing.T) {
	cfg, workspace, r := startupFixture(t)
	cfg.Command = []string{"pull"}
	var out, stderr bytes.Buffer
	if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r); code != 0 {
		t.Fatalf("code=%d: %s", code, &stderr)
	}
	if len(r.runs) != 1 || !containsSequence(r.runs[0], []string{"docker", "pull", r.reference}) || r.containers[0].State.Running {
		t.Fatalf("pull did more than fetch final rendered image: %#v", r.runs)
	}
}

func TestStoppedRefreshRechecksOverlayChanges(t *testing.T) {
	for _, changeImage := range []bool{false, true} {
		t.Run(fmt.Sprintf("image=%t", changeImage), func(t *testing.T) {
			cfg, workspace, r := startupFixture(t)
			r.onPull = func() {
				if changeImage {
					r.reference = "registry.test/other:latest"
				} else {
					r.hash = strings.Repeat("b", 64)
				}
			}
			var out, stderr bytes.Buffer
			if code := runOperational(cfg, workspace, Streams{Out: &out, Err: &stderr}, r); code != 0 {
				t.Fatalf("code=%d: %s", code, &stderr)
			}
			if r.containers[0].ID != "original" || !r.containers[0].State.Running {
				t.Fatalf("configuration changed during pull replaced the original: %#v", r.runs)
			}
		})
	}
}
