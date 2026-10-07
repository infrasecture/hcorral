package integration

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/identity"
)

const (
	threadA       = "019a1234-1111-7111-8111-111111111111"
	threadB       = "019a1234-2222-7222-8222-222222222222"
	threadC       = "019a1234-3333-7333-8333-333333333333"
	containerHome = "/home/transfer user"
	transferLabel = "ai.infrasecture.hcorral.transfer"
)

type transferResult struct {
	Operation string `json:"operation"`
	Result    struct {
		ThreadID    string `json:"thread_id"`
		MainPath    string `json:"main_path"`
		Destination string `json:"destination"`
		Files       []struct {
			Path     string `json:"path"`
			Created  bool   `json:"created"`
			Prefix   bool   `json:"prefix"`
			Promoted bool   `json:"promoted"`
		} `json:"files"`
	} `json:"result"`
}

type dockerSession struct {
	t                                                          *testing.T
	binary, image, root, hostHome, volume, container, uid, gid string
	env                                                        []string
}

// These tests run the built launcher, Docker CLI, actual helper processes and
// persistent Docker mounts. Ordinary go test skips them; the integration runner
// requires both artifact and image explicitly and never substitutes a mock.
func newDockerSession(t *testing.T, uid, gid string, running, readOnly bool) *dockerSession {
	t.Helper()
	binary, image := os.Getenv("HCORRAL_TEST_BINARY"), os.Getenv("HCORRAL_SESSION_TEST_IMAGE")
	if binary == "" || image == "" {
		t.Skip("requires HCORRAL_TEST_BINARY and HCORRAL_SESSION_TEST_IMAGE")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("test launcher path must be absolute")
	}
	f := &dockerSession{t: t, binary: binary, image: image, root: t.TempDir(), uid: uid, gid: gid}
	f.hostHome = filepath.Join(f.root, "isolated host home")
	workspace := filepath.Join(f.root, "client workspace")
	for _, dir := range []string{f.hostHome, workspace} {
		must(t, os.MkdirAll(dir, 0o700))
	}
	w, err := identity.Resolve(workspace, "", "codex", "")
	must(t, err)
	f.container, f.volume = w.Project, w.Project+"-sessions"
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if strings.HasPrefix(key, "HCORRAL_") || strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "COMPOSE_") || key == "HOME" || strings.HasPrefix(key, "XDG_") {
			continue
		}
		f.env = append(f.env, item)
	}
	f.env = append(f.env, "HOME="+f.hostHome, "XDG_CONFIG_HOME="+filepath.Join(f.root, "config"), "XDG_CACHE_HOME="+filepath.Join(f.root, "cache"), "HCORRAL_WORKSPACE="+workspace, "HCORRAL_HARNESS=codex", "HCORRAL_GUI=none", "HCORRAL_UPDATE_CHECK=false")
	// Isolate Codex/launcher state without changing the selected Docker context
	// (including Colima or a deliberately remote test daemon).
	if os.Getenv("DOCKER_CONFIG") == "" {
		home, err := os.UserHomeDir()
		must(t, err)
		f.env = append(f.env, "DOCKER_CONFIG="+filepath.Join(home, ".docker"))
	}
	f.docker("volume", "create", f.volume)
	t.Cleanup(func() {
		// Only this test's unique storage and containers are eligible. Helpers
		// cannot be removed by a broad global prefix when another test is live.
		for _, id := range f.helperIDs() {
			f.cleanupDocker("rm", "--force", id)
		}
		f.cleanupDocker("rm", "--force", f.container)
		f.cleanupDocker("volume", "rm", f.volume)
	})
	f.seed(map[string][]byte{
		".codex/" + rolloutPath(threadA): rollout(threadA, "saved container conversation"),
		".codex/" + rolloutPath(threadC): rollout(threadC, "unrelated conversation"),
		".codex/auth.json":               []byte("container credential sentinel"),
		".codex/config.toml":             []byte("# container config sentinel\n"),
		".codex/history.jsonl":           []byte("global history sentinel\n"),
		"custom-startup":                 []byte("preserve existing home\n"),
	})
	labels := map[string]string{
		identity.LabelWorkspaceID: w.FullID, identity.LabelWorkspaceScheme: "v1",
		identity.LabelCorralID: w.CorralID, identity.LabelCorralScheme: "v1",
		identity.LabelHarnessType: "codex", identity.LabelRuntimeSchema: "1",
		"com.docker.compose.project": w.Project, "com.docker.compose.service": "hcorral",
	}
	args := []string{"create", "--name", f.container, "--network", "none", "--tmpfs", "/unrelated-image-volume", "--entrypoint", "/bin/sleep"}
	for key, value := range labels {
		args = append(args, "--label", key+"="+value)
	}
	for _, item := range []string{"HCORRAL_HOST_UID=" + uid, "HCORRAL_HOST_GID=" + gid, "HCORRAL_HOST_GROUPS=" + gid + ":primary,44444:extra", "HCORRAL_CONTAINER_HOME=" + containerHome, "HCORRAL_WORKDIR=/client-workspace-not-mounted"} {
		args = append(args, "--env", item)
	}
	mount := "type=volume,src=" + f.volume + ",dst=" + containerHome
	if readOnly {
		mount += ",readonly"
	}
	args = append(args, "--mount", mount, image, "600")
	f.docker(args...)
	f.docker("start", f.container)
	if !running {
		f.docker("stop", "--time", "1", f.container)
	}
	return f
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (f *dockerSession) command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env, cmd.Dir = f.env, f.root
	return cmd
}

func (f *dockerSession) docker(args ...string) []byte {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := f.command(ctx, "docker", args...).CombinedOutput()
	if err != nil {
		f.t.Fatalf("docker %q: %v\n%s", args, err, out)
	}
	return out
}

func (f *dockerSession) cleanupDocker(args ...string) {
	f.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := f.command(ctx, "docker", args...).CombinedOutput(); err != nil {
		f.t.Errorf("cleanup docker %q: %v\n%s", args, err, out)
	}
}

func (f *dockerSession) seed(files map[string][]byte) {
	f.t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	dirs := map[string]bool{}
	for name := range files {
		for dir := path.Dir(name); dir != "."; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	var names []string
	for name := range dirs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		must(f.t, w.WriteHeader(&tar.Header{Name: name + "/", Typeflag: tar.TypeDir, Mode: 0o700}))
	}
	for name, data := range files {
		must(f.t, w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}))
		_, err := w.Write(data)
		must(f.t, err)
	}
	must(f.t, w.Close())
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := f.command(ctx, "docker", "run", "--rm", "--interactive", "--network", "none", "--tmpfs", "/unrelated-image-volume", "--mount", "type=volume,src="+f.volume+",dst="+containerHome, "--entrypoint", "/bin/sh", f.image, "-c", `tar -xf - -C "$1" && chown -R "$2:$3" "$1"`, "sh", containerHome, f.uid, f.gid)
	cmd.Stdin = &buf
	out, err := cmd.CombinedOutput()
	if err != nil {
		f.t.Fatalf("seed disposable volume: %v\n%s", err, out)
	}
}

func rolloutPath(id string) string {
	return "sessions/2026/10/06/rollout-2026-10-06T12-34-56-" + id + ".jsonl"
}

func rollout(id, text string) []byte {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	_ = enc.Encode(map[string]any{"type": "session_meta", "payload": map[string]any{"id": id, "timestamp": "2026-10-06T12:34:56Z", "cli_version": "0.160.0", "history_mode": "legacy", "cwd": "/saved workspace"}})
	_ = enc.Encode(map[string]any{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": text}}}})
	return out.Bytes()
}

func writeSession(t *testing.T, home, id, text string) []byte {
	t.Helper()
	data := rollout(id, text)
	name := filepath.Join(home, rolloutPath(id))
	must(t, os.MkdirAll(filepath.Dir(name), 0o700))
	must(t, os.WriteFile(name, data, 0o600))
	return data
}

func (f *dockerSession) transfer(env []string, operation, id, home string, wantError string) transferResult {
	f.t.Helper()
	args := []string{"session", operation, id, "--format=json"}
	if home != "" {
		args = append(args, home)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := f.command(ctx, f.binary, args...)
	cmd.Env = append(cmd.Env, env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	err := cmd.Run()
	if wantError != "" {
		if err == nil || !strings.Contains(stderr.String(), wantError) {
			f.t.Fatalf("wanted %q failure, got %v stdout=%s stderr=%s", wantError, err, &out, &stderr)
		}
		return transferResult{}
	}
	if err != nil {
		f.t.Fatalf("%s: %v stdout=%s stderr=%s", operation, err, &out, &stderr)
	}
	var result transferResult
	must(f.t, json.Unmarshal(out.Bytes(), &result))
	if result.Operation != operation || result.Result.ThreadID != id || len(result.Result.Files) == 0 {
		f.t.Fatalf("invalid report: %s", &out)
	}
	return result
}

func (f *dockerSession) containerFile(name string) ([]byte, *tar.Header) {
	f.t.Helper()
	stream := f.docker("cp", f.container+":"+containerHome+"/"+name, "-")
	r := tar.NewReader(bytes.NewReader(stream))
	header, err := r.Next()
	must(f.t, err)
	data, err := io.ReadAll(r)
	must(f.t, err)
	if _, err := r.Next(); err != io.EOF {
		f.t.Fatalf("unexpected extra copied file: %v", err)
	}
	return data, header
}

func (f *dockerSession) helperIDs() []string {
	return strings.Fields(string(f.docker("ps", "-aq", "--filter", "label="+transferLabel, "--filter", "volume="+f.volume)))
}

func (f *dockerSession) state() string {
	return string(f.docker("inspect", "--format", `{{.Id}}|{{.Image}}|{{.State.Status}}|{{.State.StartedAt}}|{{.State.FinishedAt}}|{{json .Mounts}}`, f.container))
}

func (f *dockerSession) assertPreserved(before, volumes string) {
	f.t.Helper()
	if after := f.state(); after != before {
		f.t.Fatalf("workstation changed:\nbefore %s\nafter %s", before, after)
	}
	if ids := f.helperIDs(); len(ids) != 0 {
		f.t.Fatalf("transfer helpers leaked: %v", ids)
	}
	if after := f.volumes(); after != volumes {
		f.t.Fatalf("transfer created/deleted persistent volumes: before=%s after=%s", volumes, after)
	}
	for name, want := range map[string]string{".codex/auth.json": "container credential sentinel", ".codex/config.toml": "# container config sentinel\n", "custom-startup": "preserve existing home\n"} {
		got, _ := f.containerFile(name)
		if string(got) != want {
			f.t.Fatalf("existing %s was changed", name)
		}
	}
	got, _ := f.containerFile(".codex/" + rolloutPath(threadC))
	if !bytes.Equal(got, rollout(threadC, "unrelated conversation")) {
		f.t.Fatal("unrelated conversation changed")
	}
}

func (f *dockerSession) volumes() string {
	volumes := strings.Fields(string(f.docker("volume", "ls", "-q")))
	sort.Strings(volumes)
	return strings.Join(volumes, "\n")
}

func TestDockerSessionEndpoints(t *testing.T) {
	for _, ids := range [][2]string{{"1000", "1000"}, {"501", "20"}, {"12345", "23456"}} {
		for _, running := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s-%s/running=%t", ids[0], ids[1], running), func(t *testing.T) {
				f := newDockerSession(t, ids[0], ids[1], running, false)
				before, volumes := f.state(), f.volumes()
				for _, selection := range []string{"explicit", "environment", "default"} {
					t.Run(selection, func(t *testing.T) {
						local := *f
						local.t = t
						f := &local
						destination := filepath.Join(f.root, "destination "+selection)
						argument := destination
						env := []string{"CODEX_HOME=" + filepath.Join(f.root, "unused environment")}
						if selection == "environment" {
							argument = ""
							env = []string{"CODEX_HOME=" + destination}
						}
						if selection == "default" {
							destination = filepath.Join(f.hostHome, ".codex")
							argument = ""
							env = nil
						}
						result := f.transfer(env, "export", threadA, argument, "")
						if result.Result.Destination != destination {
							t.Fatalf("wrong host destination: %+v", result)
						}
						data, err := os.ReadFile(filepath.Join(destination, result.Result.MainPath))
						must(t, err)
						if !bytes.Equal(data, rollout(threadA, "saved container conversation")) {
							t.Fatal("history bytes changed")
						}
						info, err := os.Stat(filepath.Join(destination, result.Result.MainPath))
						must(t, err)
						if info.Mode().Perm() != 0o600 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
							t.Fatal("incorrect host destination ownership/mode")
						}
						for _, excluded := range []string{"auth.json", "config.toml", "history.jsonl", rolloutPath(threadC)} {
							if _, err := os.Stat(filepath.Join(destination, excluded)); !os.IsNotExist(err) {
								t.Fatalf("copied excluded state %s: %v", excluded, err)
							}
						}
						// Repeating the transfer preserves the existing inode, mode and bytes.
						second := f.transfer(env, "export", threadA, argument, "")
						after, err := os.Stat(filepath.Join(destination, result.Result.MainPath))
						must(t, err)
						if second.Result.Files[0].Created || !os.SameFile(info, after) {
							t.Fatal("repeat export replaced history")
						}
					})
				}
				source := filepath.Join(f.root, "host import source")
				data := writeSession(t, source, threadB, "host conversation")
				must(t, os.WriteFile(filepath.Join(source, "auth.json"), []byte("host credential sentinel"), 0o600))
				result := f.transfer(nil, "import", threadB, source, "")
				copied, header := f.containerFile(".codex/" + result.Result.MainPath)
				if !bytes.Equal(copied, data) || strconv.Itoa(header.Uid) != ids[0] || strconv.Itoa(header.Gid) != ids[1] || header.Mode&0o777 != 0o600 {
					t.Fatalf("wrong imported bytes/identity: %+v", header)
				}
				f.transfer(nil, "import", threadB, source, "")
				writeSession(t, source, threadB, "divergent history must be refused")
				f.transfer(nil, "import", threadB, source, "conflict")
				copied, _ = f.containerFile(".codex/" + result.Result.MainPath)
				if !bytes.Equal(copied, data) {
					t.Fatal("conflicting import changed history")
				}
				f.assertPreserved(before, volumes)
			})
		}
	}
}

func TestDockerSessionReadOnlyStorageRefused(t *testing.T) {
	f := newDockerSession(t, "12345", "23456", false, true)
	before, volumes := f.state(), f.volumes()
	f.transfer(nil, "export", threadA, filepath.Join(f.root, "destination"), "read-only")
	f.assertPreserved(before, volumes)
}

// A separate real container owns a kernel lock. This makes blocked publication
// deterministic without a large-file timing race or a fake Docker command.
func (f *dockerSession) block(lockPath string) string {
	f.t.Helper()
	name := f.container + "-blocker"
	f.docker("run", "--detach", "--name", name, "--user", f.uid+":"+f.gid, "--network", "none", "--tmpfs", "/unrelated-image-volume", "--mount", "type=volume,src="+f.volume+",dst="+containerHome, "--entrypoint", "/bin/sh", f.image, "-c", `mkdir -p "$(dirname "$1")"; exec flock -x "$1" sh -c 'echo locked > "$1"; exec sleep 300' sh "$2"`, "sh", containerHome+"/.codex/"+lockPath, containerHome+"/locked")
	f.t.Cleanup(func() { f.cleanupDocker("rm", "--force", name) })
	f.waitFor("lock holder", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return f.command(ctx, "docker", "exec", name, "test", "-f", containerHome+"/locked").Run() == nil
	})
	return name
}

func (f *dockerSession) waitFor(what string, ready func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	f.t.Fatalf("did not observe %s", what)
}

func TestDockerSessionCancellationStopsRemoteHelper(t *testing.T) {
	f := newDockerSession(t, "12345", "23456", false, false)
	before, volumes := f.state(), f.volumes()
	source := filepath.Join(f.root, "source")
	writeSession(t, source, threadB, "cancellable import")
	blocker := f.block(".hcorral-staging.lock")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := f.command(ctx, f.binary, "session", "import", threadB, source, "--format=json")
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	must(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			t.Error("launcher did not exit")
		}
	})
	f.waitFor("running transfer helper", func() bool {
		ids := f.helperIDs()
		if len(ids) != 1 {
			return false
		}
		return strings.TrimSpace(string(f.docker("inspect", "--format", "{{.State.Running}}", ids[0]))) == "true"
	})
	must(t, cmd.Process.Signal(syscall.SIGTERM))
	select {
	case err := <-done:
		done <- err // Keep the cleanup wait idempotent.
		var exit *exec.ExitError
		if err == nil {
			t.Fatal("cancelled transfer succeeded")
		}
		if !errors.As(err, &exit) || exit.ExitCode() != 130 {
			t.Fatalf("cancellation exit: %v\n%s", err, &output)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("launcher did not finish remote cancellation")
	}
	if len(f.helperIDs()) != 0 {
		t.Fatal("remote helper survived cancellation")
	}
	if strings.TrimSpace(string(f.docker("inspect", "--format", "{{.State.Running}}", blocker))) != "true" {
		t.Fatal("unrelated lock owner was stopped")
	}
	f.docker("stop", "--time", "1", blocker)
	result := f.transfer(nil, "import", threadB, source, "")
	got, _ := f.containerFile(".codex/" + result.Result.MainPath)
	if !bytes.Equal(got, rollout(threadB, "cancellable import")) {
		t.Fatal("retry did not import complete history")
	}
	f.assertPreserved(before, volumes)
}

func TestDockerSessionActiveWriterRefused(t *testing.T) {
	f := newDockerSession(t, "501", "20", false, false)
	before, volumes := f.state(), f.volumes()
	blocker := f.block("thread-writer-locks/" + threadA + ".lock")
	f.transfer(nil, "export", threadA, filepath.Join(f.root, "destination"), "busy")
	f.docker("stop", "--time", "1", blocker)
	f.transfer(nil, "export", threadA, filepath.Join(f.root, "destination"), "")
	f.assertPreserved(before, volumes)
}
