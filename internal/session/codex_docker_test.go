package session

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/infrasecture/hcorral/internal/identity"
)

func nativeEndpointHome(t *testing.T) *Home {
	t.Helper()
	name := filepath.Join(t.TempDir(), ".codex")
	if err := os.Mkdir(name, 0o700); err != nil {
		t.Fatal(err)
	}
	home, err := OpenHome(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { home.Close() })
	return home
}

// Native compatibility uses the actual storage pipeline without Docker.
type endpointTransfer func(*testing.T, *Home, *Home, string, bool) Result

func nativeEndpointTransfer(t *testing.T, source, destination *Home, id string, _ bool) Result {
	t.Helper()
	return published(t, received(t, destination, exported(t, source, id)))
}

// Only these two journeys compose native Codex with the public Docker command.
// History format and existing-home combinations belong to native qualification.
func TestDockerCodexRoundTrip(t *testing.T) {
	if os.Getenv("HCORRAL_TEST_CODEX") == "" {
		t.Skip("requires native Codex and Docker qualification artifacts")
	}
	testCodexResumesNativeHistory(t, dockerEndpointTransfer, []string{"archived-revert-prefix"}, []bool{true})
}

func TestDockerCodexActiveTurn(t *testing.T) {
	if os.Getenv("HCORRAL_TEST_CODEX") == "" {
		t.Skip("requires native Codex and Docker qualification artifacts")
	}
	testNativeCodexCopiesDuringRunningTurn(t, dockerEndpointTransfer, []string{"inherited"})
}

func dockerEndpointTransfer(t *testing.T, source, destination *Home, id string, fromContainer bool) Result {
	t.Helper()
	binary, image := os.Getenv("HCORRAL_TEST_BINARY"), os.Getenv("HCORRAL_SESSION_TEST_IMAGE")
	if !filepath.IsAbs(binary) || image == "" {
		t.Fatal("native Docker qualification requires the packaged launcher and explicit fixture image")
	}
	workspace := t.TempDir()
	w, err := identity.Resolve(workspace, "", "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	volume := w.Project + "-native"
	containerHome := filepath.Dir(source.Path)
	uid, gid := strconv.Itoa(os.Geteuid()), strconv.Itoa(os.Getegid())
	hostHome := t.TempDir()
	var environment []string
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if key == "HOME" || strings.HasPrefix(key, "HCORRAL_") || strings.HasPrefix(key, "CODEX_") || strings.HasPrefix(key, "COMPOSE_") || strings.HasPrefix(key, "XDG_") {
			continue
		}
		environment = append(environment, item)
	}
	if os.Getenv("DOCKER_CONFIG") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Fatal(err)
		}
		environment = append(environment, "DOCKER_CONFIG="+filepath.Join(home, ".docker"))
	}
	environment = append(environment, "HOME="+hostHome, "HCORRAL_WORKSPACE="+workspace, "HCORRAL_HARNESS=codex", "HCORRAL_GUI=none", "HCORRAL_UPDATE_CHECK=false")
	run := func(name string, input io.Reader, args ...string) ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, name, args...)
		command.Env, command.Dir, command.Stdin = environment, workspace, input
		var out, stderr bytes.Buffer
		command.Stdout, command.Stderr = &out, &stderr
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("%s %q: %w\n%s\n%s", name, args, err, &out, &stderr)
		}
		return out.Bytes(), nil
	}
	docker := func(input io.Reader, args ...string) []byte {
		t.Helper()
		out, err := run("docker", input, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	docker(nil, "volume", "create", volume)
	t.Cleanup(func() {
		// Exact fixture storage identifies any interrupted helper. Never sweep
		// helpers belonging to another concurrent test or user operation.
		out, err := run("docker", nil, "ps", "-aq", "--filter", "volume="+volume, "--filter", "label=ai.infrasecture.hcorral.transfer")
		if err != nil {
			t.Error(err)
		}
		for _, helper := range strings.Fields(string(out)) {
			if _, err := run("docker", nil, "rm", "--force", helper); err != nil {
				t.Error(err)
			}
		}
		if _, err := run("docker", nil, "rm", "--force", w.Project); err != nil {
			t.Error(err)
		}
		if _, err := run("docker", nil, "volume", "rm", volume); err != nil {
			t.Error(err)
		}
	})
	mount := "type=volume,src=" + volume + ",dst=" + containerHome
	seed := &bytes.Buffer{}
	writer := tar.NewWriter(seed)
	if err := writer.WriteHeader(&tar.Header{Name: ".codex/", Typeflag: tar.TypeDir, Mode: 0o700, Uid: os.Geteuid(), Gid: os.Getegid()}); err != nil {
		t.Fatal(err)
	}
	if fromContainer {
		// Fixture setup copies only this test's synthetic source, after its
		// database is closed. Preserve absolute metadata paths by mounting the
		// independent Docker volume at the same spelling, not a host bind.
		err := filepath.WalkDir(source.Path, func(name string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil || name == source.Path {
				return walkErr
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				return fmt.Errorf("unexpected native fixture entry %s", name)
			}
			relative, err := filepath.Rel(source.Path, name)
			if err != nil {
				return err
			}
			header, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			header.Name, header.Uid, header.Gid = ".codex/"+filepath.ToSlash(relative), os.Geteuid(), os.Getegid()
			if err := writer.WriteHeader(header); err != nil || info.IsDir() {
				return err
			}
			data, err := os.ReadFile(name)
			if err == nil {
				_, err = writer.Write(data)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	docker(seed, "run", "--rm", "--interactive", "--network", "none", "--tmpfs", "/unrelated-image-volume", "--mount", mount, "--entrypoint", "/bin/tar", image, "-xf", "-", "-C", containerHome)
	args := []string{"create", "--name", w.Project, "--network", "none", "--tmpfs", "/unrelated-image-volume", "--mount", mount, "--entrypoint", "/bin/sleep"}
	for key, value := range map[string]string{
		identity.LabelWorkspaceID: w.FullID, identity.LabelWorkspaceScheme: "v1", identity.LabelCorralID: w.CorralID,
		identity.LabelCorralScheme: "v1", identity.LabelHarnessType: "codex", identity.LabelRuntimeSchema: "1",
		"com.docker.compose.project": w.Project, "com.docker.compose.service": "hcorral",
	} {
		args = append(args, "--label", key+"="+value)
	}
	for _, item := range []string{"HCORRAL_HOST_UID=" + uid, "HCORRAL_HOST_GID=" + gid, "HCORRAL_HOST_GROUPS=" + gid + ":primary", "HCORRAL_CONTAINER_HOME=" + containerHome, "HCORRAL_WORKDIR=/"} {
		args = append(args, "--env", item)
	}
	docker(nil, append(args, image, "600")...)
	state := func() string {
		out := docker(nil, "inspect", "--format", `{{.Id}}|{{.Image}}|{{.State.Status}}|{{.State.StartedAt}}|{{.State.FinishedAt}}{{println}}{{range .Mounts}}{{json .}}{{println}}{{end}}`, w.Project)
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		sort.Strings(lines)
		return strings.Join(lines, "\n")
	}
	before := state()
	transfer := func(operation, home string) Result {
		out, err := run(binary, nil, "session", operation, id, home, "--format=json")
		if err != nil {
			t.Fatal(err)
		}
		var report struct {
			Result Result `json:"result"`
		}
		if err := json.Unmarshal(out, &report); err != nil || report.Result.ThreadID != id {
			t.Fatalf("invalid public transfer result: %s (%v)", out, err)
		}
		if after := state(); after != before {
			t.Fatalf("public transfer changed the stopped workstation:\nbefore %s\nafter %s", before, after)
		}
		if helpers := strings.TrimSpace(string(docker(nil, "ps", "-aq", "--filter", "volume="+volume, "--filter", "label=ai.infrasecture.hcorral.transfer"))); helpers != "" {
			t.Fatalf("public transfer leaked helpers: %s", helpers)
		}
		return report.Result
	}
	if !fromContainer {
		transfer("import", source.Path)
	}
	return transfer("export", destination.Path)
}
