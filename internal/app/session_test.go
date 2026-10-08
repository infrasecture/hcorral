package app

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/session"
	"github.com/infrasecture/hcorral/internal/sessionconfig"
	"github.com/infrasecture/hcorral/internal/sessionhelper"
)

const appSessionID = "019a1234-1111-7111-8111-111111111111"
const appSessionFile = "sessions/2026/10/06/rollout-2026-10-06T12-34-56-" + appSessionID + ".jsonl"
const transferOwnerLabel = "ai.infrasecture.hcorral.transfer"

func TestSessionFailureDistinguishesCallerAndPeerCancellation(t *testing.T) {
	failure := errors.Join(session.ErrConflict, context.Canceled)
	if code := failSession(context.Background(), io.Discard, failure); code != 1 {
		t.Fatalf("peer cleanup cancellation hid the conflict: exit %d", code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if code := failSession(ctx, io.Discard, failure); code != 130 {
		t.Fatalf("caller cancellation lost: exit %d", code)
	}
}

func TestSessionArgumentsAcceptIntermixedFlagsAndPreserveHostPaths(t *testing.T) {
	for _, args := range [][]string{
		{"export", appSessionID, "host with spaces", "--format=json", "--files", "5000"},
		{"export", "--files=5000", "--format", "json", appSessionID, "host with spaces"},
	} {
		r, err := parseSession(args)
		if err != nil || r.hostHome != "host with spaces" || r.format != "json" || r.limits.Files != 5000 {
			t.Fatalf("request: %+v %v", r, err)
		}
	}
	for _, args := range [][]string{{}, {"sync", appSessionID}, {"import", "../bad"}, {"export", appSessionID, "one", "two"}, {"export", appSessionID, "--bogus"}, {"export", appSessionID, "--files=0"}, {"export", appSessionID, "--container-sqlite-home=relative"}, {"export", appSessionID, "--host-sqlite-home="}, {"export", appSessionID, "--format=xml"}} {
		if _, err := parseSession(args); err == nil {
			t.Fatalf("accepted invalid arguments: %q", args)
		}
	}
}

type sessionRunner struct {
	workstation *containerruntime.Container
	helper      *containerruntime.Container
	helperArgs  []string
	commands    [][]string
	configFiles map[string]string
	cleanupErr  error
}

func sessionJSON(value any) command.Result {
	data, _ := json.Marshal(value)
	return command.Result{Stdout: data}
}
func argument(args []string, key string) string {
	for i, arg := range args {
		if arg == key && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func (f *sessionRunner) Capture(ctx context.Context, args, env []string) (command.Result, error) {
	if err := ctx.Err(); err != nil {
		return command.Result{}, err
	}
	f.commands = append(f.commands, append([]string(nil), args...))
	switch strings.Join(args[:min(3, len(args))], " ") {
	case "docker ps -aq":
		if f.workstation == nil {
			return command.Result{}, nil
		}
		return command.Result{Stdout: []byte(f.workstation.ID)}, nil
	case "docker inspect --type":
		name := args[len(args)-1]
		for _, c := range []*containerruntime.Container{f.workstation, f.helper} {
			if c != nil && (name == c.ID || name == c.CleanName()) {
				return sessionJSON([]containerruntime.Container{*c}), nil
			}
		}
		return command.Result{Stderr: []byte("No such container")}, errors.New("not found")
	case "docker volume inspect":
		return sessionJSON([]containerruntime.Volume{{Name: args[3], Labels: identity.SharedVolumeLabels()}}), nil
	case "docker image inspect":
		return sessionJSON([]containerruntime.Image{{ID: f.workstation.ImageID, OS: "linux", Architecture: "amd64"}}), nil
	case "docker create --name":
		f.helper = &containerruntime.Container{ID: strings.Repeat("b", 64), Name: "/" + argument(args, "--name")}
		_, token, _ := strings.Cut(argument(args, "--label"), "=")
		f.helper.Config.Labels = map[string]string{transferOwnerLabel: token}
		f.helper.State.Status = "created"
		for i, arg := range args {
			if arg == f.workstation.ImageID {
				f.helperArgs = append([]string(nil), args[i+1:]...)
				break
			}
		}
		return command.Result{Stdout: []byte(f.helper.ID)}, nil
	default:
		return command.Result{}, fmt.Errorf("unexpected Capture: %q", args)
	}
}

func (f *sessionRunner) Run(ctx context.Context, args, env []string, input io.Reader, output, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.commands = append(f.commands, append([]string(nil), args...))
	switch args[1] {
	case "cp":
		if args[2] == "-L" {
			_, name, _ := strings.Cut(args[3], ":")
			data, ok := f.configFiles[name]
			if !ok {
				fmt.Fprintf(stderr, "Error response from daemon: Could not find the file %s in container %s", name, f.workstation.ID)
				return os.ErrNotExist
			}
			w := tar.NewWriter(output)
			if err := w.WriteHeader(&tar.Header{Name: filepath.Base(name), Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
				return err
			}
			if _, err := io.WriteString(w, data); err != nil {
				return err
			}
			return w.Close()
		}
		if args[3] != f.helper.ID+":/" {
			return errors.New("copied helper into workstation")
		}
		r := tar.NewReader(input)
		header, err := r.Next()
		if err != nil || header.Name != "hcorral-session" {
			return errors.New("invalid helper archive")
		}
		_, err = io.Copy(io.Discard, r)
		return err
	case "start":
		if f.helper == nil || args[len(args)-1] != f.helper.ID {
			return errors.New("started workstation")
		}
		f.helper.State.Status, f.helper.State.Running = "running", true
		err := sessionhelper.Run(ctx, f.helperArgs, input, output)
		f.helper.State.Status, f.helper.State.Running = "exited", false
		if err != nil {
			f.helper.State.ExitCode = 1
		}
		return err
	case "stop":
		if f.helper == nil || args[len(args)-1] != f.helper.ID {
			return errors.New("stopped workstation")
		}
		f.helper.State.Status, f.helper.State.Running = "exited", false
		return nil
	case "rm":
		if f.helper == nil || args[len(args)-1] != f.helper.ID || len(args) != 3 {
			return errors.New("removed workstation or volumes")
		}
		if f.cleanupErr != nil {
			return f.cleanupErr
		}
		f.helper = nil
		return nil
	default:
		return fmt.Errorf("unexpected Run: %q", args)
	}
}
func (*sessionRunner) Replace([]string, []string) error {
	return errors.New("attached instead of transferring")
}

func sessionFixture(t *testing.T) (config.Config, identity.Workspace, *sessionRunner, sessionServices, string, string) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", root)
	workspace, err := identity.Resolve(root, "", "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	containerHome := filepath.Join(root, "container home")
	if err := os.Mkdir(containerHome, 0o700); err != nil {
		t.Fatal(err)
	}
	containerCodex := filepath.Join(containerHome, ".codex")
	host := filepath.Join(root, "host codex")
	c := &containerruntime.Container{ID: strings.Repeat("a", 64), ImageID: "sha256:" + strings.Repeat("c", 64), Name: "/" + workspace.Project}
	c.Config.Labels = ownedLabels(workspace, "wayland")
	c.Config.Image = "fixture:latest"
	c.Config.Env = []string{"HCORRAL_HOST_UID=" + strconv.Itoa(os.Geteuid()), "HCORRAL_HOST_GID=" + strconv.Itoa(os.Getegid()), "HCORRAL_HOST_GROUPS=" + strconv.Itoa(os.Getegid()) + ":primary", "HCORRAL_CONTAINER_HOME=" + containerHome, "HCORRAL_WORKDIR=" + root}
	c.Mounts = []containerruntime.Mount{{Type: "volume", Name: "existing-state", Destination: containerHome, RW: true}, {Type: "bind", Source: "/daemon/project", Destination: root, RW: true}}
	c.State.Status, c.State.Running = "running", true
	runner := &sessionRunner{workstation: c, configFiles: map[string]string{}}
	services := sessionServices{home: filepath.Join(root, "default user"), codexHome: host, readHost: func(ctx context.Context, name string) ([]byte, error) {
		if !strings.HasPrefix(name, root+"/") {
			return nil, os.ErrNotExist
		}
		return sessionconfig.ReadLocalFile(ctx, name)
	}, helper: func(string) ([]byte, error) { return []byte("fixture supplied helper"), nil }}
	cfg := testConfig(workspace)
	cfg.Command = []string{"session", "export", appSessionID}
	return cfg, workspace, runner, services, host, containerCodex
}

func writeAppSession(t *testing.T, home, message string) []byte {
	t.Helper()
	data := []byte(`{"type":"session_meta","payload":{"id":"` + appSessionID + `","timestamp":"2026-10-06T12:34:56Z","history_mode":"legacy","cli_version":"0.160.0","cwd":"/saved workspace"}}` + "\n" +
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"` + message + `"}]}}` + "\n")
	name := filepath.Join(home, appSessionFile)
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("private authentication fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func TestSessionCommandsTransferThroughBothEndpointsWithoutWorkstationLifecycle(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		for _, state := range []string{"running", "exited"} {
			for _, selection := range []string{"explicit", "environment", "default"} {
				t.Run(operation+"/"+state+"/"+selection, func(t *testing.T) {
					cfg, workspace, runner, services, host, container := sessionFixture(t)
					cfg.Command = []string{"session", operation, appSessionID, "--format=json"}
					switch selection {
					case "explicit":
						cfg.Command = append(cfg.Command, host)
						services.codexHome = "/must-not-use"
					case "default":
						services.codexHome = ""
						host = filepath.Join(services.home, ".codex")
					}
					source, destination := container, host
					if operation == "import" {
						source, destination = host, container
					}
					data := writeAppSession(t, source, "original conversation")
					runner.workstation.State.Status, runner.workstation.State.Running = state, state == "running"
					var out, stderr bytes.Buffer
					code := runSession(context.Background(), cfg, workspace, Streams{Out: &out, Err: &stderr}, runner, services)
					if code != 0 {
						t.Fatalf("exit=%d stderr=%s", code, stderr.String())
					}
					got, err := os.ReadFile(filepath.Join(destination, appSessionFile))
					if err != nil || !bytes.Equal(got, data) {
						t.Fatalf("transferred history: %v", err)
					}
					var report struct {
						Operation string         `json:"operation"`
						Result    session.Result `json:"result"`
					}
					if err := json.Unmarshal(out.Bytes(), &report); err != nil || report.Operation != operation || report.Result.ThreadID != appSessionID {
						t.Fatalf("result: %s %v", out.String(), err)
					}
					if _, err := os.Stat(filepath.Join(destination, "auth.json")); !os.IsNotExist(err) {
						t.Fatal("authentication was copied")
					}
					if runner.workstation.State.Status != state || runner.helper != nil {
						t.Fatal("workstation state changed or helper was retained")
					}
					configReads := 0
					for _, args := range runner.commands {
						if len(args) > 2 && args[1] == "cp" && args[2] == "-L" {
							configReads++
						}
						if len(args) > 1 && (args[1] == "compose" || args[1] == "pull" || args[1] == "exec") {
							t.Fatalf("transfer invoked lifecycle/login setup: %q", args)
						}
					}
					// Six normal config candidates give 24 Docker invocations.
					// Deeper workspaces add file reads, not repeated inspections.
					if len(runner.commands) > 18+configReads {
						t.Fatalf("transfer exceeded its Docker command budget: %d commands, %d config reads", len(runner.commands), configReads)
					}
				})
			}
		}
	}
}

func TestSessionCommandUsesRelocatedSQLiteConfig(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		t.Run(operation, func(t *testing.T) {
			cfg, workspace, runner, services, host, container := sessionFixture(t)
			cfg.Command = []string{"session", operation, appSessionID}
			source := container
			if operation == "import" {
				source = host
			}
			data := writeAppSession(t, source, "current selected conversation")
			old := strings.Replace(appSessionFile, "12-34-56", "11-34-56", 1)
			if err := os.WriteFile(filepath.Join(source, old), bytes.Replace(data, []byte("current selected"), []byte("stale unselected"), 1), 0o600); err != nil {
				t.Fatal(err)
			}
			state := filepath.Join(filepath.Dir(source), "separate state")
			if err := os.MkdirAll(state, 0o700); err != nil {
				t.Fatal(err)
			}
			db, err := sql.Open("sqlite", filepath.Join(state, "state_5.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("CREATE TABLE threads(id TEXT PRIMARY KEY, rollout_path TEXT, archived INTEGER, history_mode TEXT)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO threads VALUES(?,?,0,'legacy')", appSessionID, filepath.Join(source, appSessionFile)); err != nil {
				t.Fatal(err)
			}
			db.Close()
			config := "sqlite_home='../separate state'\n"
			if operation == "export" {
				runner.configFiles[filepath.Join(source, "config.toml")] = config
			} else {
				if err := os.WriteFile(filepath.Join(source, "config.toml"), []byte(config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, stderr bytes.Buffer
			if code := runSession(context.Background(), cfg, workspace, Streams{Out: &out, Err: &stderr}, runner, services); code != 0 {
				t.Fatalf("exit=%d %s", code, stderr.String())
			}
			destination := host
			if operation == "import" {
				destination = container
			}
			got, err := os.ReadFile(filepath.Join(destination, appSessionFile))
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("did not use authoritative relocated metadata: %v", err)
			}
		})
	}
}

func TestSessionCommandRefusesMismatchedStateAndUnsupportedTargetsBeforeMutation(t *testing.T) {
	for _, problem := range []string{"missing", "ownership", "state", "private", "harness", "paused", "project sqlite"} {
		t.Run(problem, func(t *testing.T) {
			cfg, workspace, runner, services, _, _ := sessionFixture(t)
			switch problem {
			case "missing":
				runner.workstation = nil
			case "ownership":
				runner.workstation.Config.Labels[identity.LabelWorkspaceID] = "foreign"
			case "state":
				cfg.StateSpecified = true
				cfg.StateMode = config.StateCustom
				cfg.StateVolumeName = "different"
			case "private":
				cfg.StateSpecified = true
				cfg.StateMode = config.StatePrivate
			case "harness":
				cfg.Harness = "claude"
			case "paused":
				runner.workstation.State.Paused = true
			case "project sqlite":
				runner.configFiles[filepath.Join(workspace.Path, ".codex/config.toml")] = "sqlite_home='/project-state'"
			}
			var out, stderr bytes.Buffer
			if code := runSession(context.Background(), cfg, workspace, Streams{Out: &out, Err: &stderr}, runner, services); code == 0 {
				t.Fatal("accepted invalid transfer")
			}
			for _, args := range runner.commands {
				if args[1] == "create" || args[1] == "start" || args[1] == "rm" {
					t.Fatalf("invalid preflight mutated runtime: %q", args)
				}
			}
		})
	}
}

func TestSessionCommandReportsConfirmedImportDespiteCleanupError(t *testing.T) {
	cfg, workspace, runner, services, host, container := sessionFixture(t)
	writeAppSession(t, host, "saved conversation")
	cfg.Command = []string{"session", "import", appSessionID, "--format=json"}
	runner.cleanupErr = errors.New("fixture cleanup failure")
	var out, stderr bytes.Buffer
	code := runSession(context.Background(), cfg, workspace, Streams{Out: &out, Err: &stderr}, runner, services)
	if code == 0 || !strings.Contains(stderr.String(), "confirmed session publication") || !strings.Contains(out.String(), appSessionID) {
		t.Fatalf("lost confirmed result: exit=%d out=%s err=%s", code, out.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(container, appSessionFile)); err != nil {
		t.Fatal("fixture did not publish")
	}
}

func TestSessionHelpDoesNotNeedWorkspaceOrDocker(t *testing.T) {
	var out, stderr bytes.Buffer
	if code := Run([]string{"--workspace", filepath.Join(t.TempDir(), "missing"), "session", "--help"}, Streams{Out: &out, Err: &stderr}); code != 0 || !strings.Contains(out.String(), "CODEX_HOME") {
		t.Fatalf("help: exit=%d %s", code, stderr.String())
	}
}
