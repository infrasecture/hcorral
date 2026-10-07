package sessiontransport

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

type dockerFixture struct {
	workstation, helper          *containerruntime.Container
	image                        *containerruntime.Image
	volume                       *containerruntime.Volume
	commands                     [][]string
	createErr, copyErr, startErr error
	exitCode                     int
	changeAfterCopy              func()
	onStart                      func()
	badCleanupOwner              bool
	configArchive, configStderr  []byte
	configErr                    error
	t                            *testing.T
}

func transportFixture(t *testing.T) (Docker, *dockerFixture, identity.Workspace, Target) {
	t.Helper()
	workspace, err := identity.Resolve(t.TempDir(), "", "codex", "")
	if err != nil {
		t.Fatal(err)
	}
	c := &containerruntime.Container{ID: strings.Repeat("1", 64), ImageID: "sha256:" + strings.Repeat("2", 64), Name: "/" + workspace.Project}
	c.Config.Image = "unrelated-moving-alias:latest"
	c.Config.Labels = map[string]string{
		identity.LabelWorkspaceID: workspace.FullID, identity.LabelWorkspaceScheme: "v1",
		identity.LabelCorralID: workspace.CorralID, identity.LabelCorralScheme: "v1",
		identity.LabelRuntimeSchema: "1", identity.LabelHarnessType: "codex",
		"com.docker.compose.project": workspace.Project, "com.docker.compose.service": "hcorral",
	}
	c.Config.Env = []string{"HCORRAL_HOST_UID=12345", "HCORRAL_HOST_GID=23456", "HCORRAL_HOST_GROUPS=44444:extra,23456:primary", "HCORRAL_CONTAINER_HOME=/home/actual user", "HCORRAL_WORKDIR=/workspace", "CODEX_SQLITE_HOME=/home/actual user/.codex"}
	c.State.Status, c.State.Running = "running", true
	c.Mounts = []containerruntime.Mount{
		{Type: "bind", Source: "/daemon/workspace", Destination: "/workspace", RW: true},
		{Type: "volume", Name: "existing-state", Source: "/daemon/volumes/state", Destination: "/home/actual user", RW: true},
		{Type: "bind", Source: "/daemon/home/git", Destination: "/home/actual user/git", RW: true},
		{Type: "bind", Source: "/daemon/desktop", Destination: "/tmp/.hcorral-wayland", RW: true},
	}
	f := &dockerFixture{t: t, workstation: c, image: &containerruntime.Image{ID: c.ImageID, OS: "linux", Architecture: "arm64"}, volume: &containerruntime.Volume{Name: "existing-state", Mountpoint: "/daemon/volumes/state", Labels: map[string]string{identity.LabelRuntimeSchema: "1"}}}
	d := Docker{Runtime: containerruntime.NewDocker(f).WithStreams(io.Discard, io.Discard), Helper: func(arch string) ([]byte, error) {
		if arch != "arm64" {
			t.Errorf("selected host architecture instead of image: %s", arch)
		}
		return []byte("arm64 helper bytes"), nil
	}}
	target, err := InspectTarget(c)
	if err != nil {
		t.Fatal(err)
	}
	return d, f, workspace, target
}

func argValue(argv []string, key string) string {
	for i, value := range argv {
		if value == key && i+1 < len(argv) {
			return argv[i+1]
		}
	}
	return ""
}

func jsonResult(value any) command.Result {
	data, _ := json.Marshal(value)
	return command.Result{Stdout: data}
}

func (f *dockerFixture) Capture(ctx context.Context, argv, env []string) (command.Result, error) {
	if err := ctx.Err(); err != nil {
		return command.Result{}, err
	}
	f.commands = append(f.commands, append([]string(nil), argv...))
	switch strings.Join(argv[:min(3, len(argv))], " ") {
	case "docker inspect --type":
		name := argv[len(argv)-1]
		for _, c := range []*containerruntime.Container{f.workstation, f.helper} {
			if c != nil && (name == c.ID || name == c.CleanName()) {
				return jsonResult([]*containerruntime.Container{c}), nil
			}
		}
		return command.Result{Stderr: []byte("No such container")}, errors.New("not found")
	case "docker image inspect":
		if f.image == nil {
			return command.Result{Stderr: []byte("No such image")}, errors.New("not found")
		}
		if argv[3] != f.workstation.ImageID {
			f.t.Fatal("inspected mutable reference instead of deployed image")
		}
		return jsonResult([]*containerruntime.Image{f.image}), nil
	case "docker volume inspect":
		if f.volume == nil {
			return command.Result{Stderr: []byte("No such volume")}, errors.New("not found")
		}
		return jsonResult([]*containerruntime.Volume{f.volume}), nil
	case "docker create --name":
		f.helper = &containerruntime.Container{ID: strings.Repeat("3", 64), Name: "/" + argValue(argv, "--name")}
		_, token, _ := strings.Cut(argValue(argv, "--label"), "=")
		f.helper.Config.Labels = map[string]string{helperLabel: token}
		f.helper.State.Status = "created"
		return command.Result{Stdout: []byte(f.helper.ID + "\n")}, f.createErr
	default:
		f.t.Fatalf("unexpected Capture: %q", argv)
		return command.Result{}, errors.New("unexpected command")
	}
}

func (f *dockerFixture) Run(ctx context.Context, argv, env []string, input io.Reader, output, stderr io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.commands = append(f.commands, append([]string(nil), argv...))
	switch argv[1] {
	case "cp":
		if argv[2] == "-L" {
			if argv[len(argv)-1] != "-" || !strings.HasPrefix(argv[3], f.workstation.ID+":/") {
				f.t.Fatal("config read did not use the inspected container")
			}
			if f.changeAfterCopy != nil {
				f.changeAfterCopy()
			}
			if _, err := stderr.Write(f.configStderr); err != nil {
				return err
			}
			if _, err := output.Write(f.configArchive); err != nil {
				return err
			}
			return f.configErr
		}
		if argv[3] != f.helper.ID+":/" {
			f.t.Fatal("helper copied into the workstation")
		}
		r := tar.NewReader(input)
		header, err := r.Next()
		if err != nil || header.Name != "hcorral-session" || header.Mode != 0o555 {
			f.t.Fatalf("bad helper member: %+v %v", header, err)
		}
		data, err := io.ReadAll(r)
		if err != nil || string(data) != "arm64 helper bytes" {
			f.t.Fatalf("bad helper data: %q %v", data, err)
		}
		if _, err := r.Next(); err != io.EOF {
			f.t.Fatal("unexpected helper archive content")
		}
		if f.changeAfterCopy != nil {
			f.changeAfterCopy()
		}
		return f.copyErr
	case "start":
		if argv[len(argv)-1] != f.helper.ID {
			f.t.Fatal("started the workstation")
		}
		f.helper.State.Status, f.helper.State.Running = "running", true
		if f.onStart != nil {
			f.onStart()
		}
		if f.startErr != nil {
			if f.badCleanupOwner {
				f.helper.Config.Labels[helperLabel] = "replaced"
			}
			return f.startErr
		}
		if input != nil {
			if _, err := io.Copy(output, input); err != nil {
				return err
			}
		}
		f.helper.State.Status, f.helper.State.Running, f.helper.State.ExitCode = "exited", false, f.exitCode
		return nil
	case "stop":
		if argv[len(argv)-1] != f.helper.ID {
			f.t.Fatal("stopped the workstation")
		}
		f.helper.State.Status, f.helper.State.Running = "exited", false
		return nil
	case "rm":
		if argv[len(argv)-1] != f.helper.ID || len(argv) != 3 {
			f.t.Fatal("removed more than the helper")
		}
		f.helper = nil
		return nil
	default:
		f.t.Fatalf("unexpected Run: %q", argv)
		return errors.New("unexpected command")
	}
}
func (*dockerFixture) Replace([]string, []string) error { return errors.New("must not attach") }

func TestTransferUsesDeployedImageAndStorageWithoutWorkstationLifecycle(t *testing.T) {
	for _, state := range []string{"running", "exited"} {
		t.Run(state, func(t *testing.T) {
			d, f, workspace, target := transportFixture(t)
			f.workstation.State.Status, f.workstation.State.Running = state, state == "running"
			var output bytes.Buffer
			if err := d.Run(context.Background(), workspace, target, []string{"protocol"}, strings.NewReader("stream"), &output, io.Discard); err != nil {
				t.Fatal(err)
			}
			if output.String() != "stream" || f.helper != nil {
				t.Fatal("stream or cleanup failed")
			}
			if f.workstation.State.Status != state {
				t.Fatal("workstation state changed")
			}
			var create []string
			for _, args := range f.commands {
				if len(args) > 1 && args[1] == "create" {
					create = args
				}
			}
			for key, value := range map[string]string{"--user": "12345:23456", "--network": "none", "--entrypoint": helperPath, "--pull": "never", "--workdir": "/", "--cap-drop": "ALL", "--stop-signal": "SIGTERM"} {
				if argValue(create, key) != value {
					t.Fatalf("%s=%q want %q", key, argValue(create, key), value)
				}
			}
			if create[len(create)-2] != target.ImageID || strings.Contains(strings.Join(create, "\n"), "latest") {
				t.Fatal("helper used a mutable image reference")
			}
			mount := argValue(create, "--mount")
			if !strings.Contains(mount, "src=existing-state") || !strings.Contains(mount, "volume-nocopy=true") {
				t.Fatalf("bad storage mount %s", mount)
			}
			for _, forbidden := range []string{"/daemon/workspace", "/daemon/home/git", "/daemon/desktop", "--tty", "--privileged"} {
				if strings.Contains(strings.Join(create, "\n"), forbidden) {
					t.Fatalf("helper inherited %s", forbidden)
				}
			}
		})
	}
}

func TestTransferPreflightHasNoMutation(t *testing.T) {
	for _, problem := range []string{"image missing", "volume missing", "wrong owner", "unsupported architecture", "changed identity", "no bundle"} {
		t.Run(problem, func(t *testing.T) {
			d, f, workspace, target := transportFixture(t)
			switch problem {
			case "image missing":
				f.image = nil
			case "volume missing":
				f.volume = nil
			case "wrong owner":
				f.volume.Labels[identity.LabelWorkspaceID] = "someone else"
			case "unsupported architecture":
				f.image.Architecture = "riscv64"
			case "changed identity":
				f.workstation.Config.Env[0] = "HCORRAL_HOST_UID=1000"
			case "no bundle":
				d.Helper = nil
			}
			if err := d.Run(context.Background(), workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("accepted invalid preflight")
			}
			for _, argv := range f.commands {
				if argv[1] != "inspect" && argv[1] != "image" && argv[1] != "volume" {
					t.Fatalf("preflight mutated Docker: %q", argv)
				}
			}
		})
	}
}

func TestTransferFailureCleansOnlyOwnedHelper(t *testing.T) {
	for _, problem := range []string{"create reply lost", "copy failed", "start failed", "nonzero exit", "changed after copy", "cancelled", "changed helper owner"} {
		t.Run(problem, func(t *testing.T) {
			d, f, workspace, target := transportFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			failure := errors.New("fixture failure")
			switch problem {
			case "create reply lost":
				f.createErr = failure
			case "copy failed":
				f.copyErr = failure
			case "start failed":
				f.startErr = failure
			case "nonzero exit":
				f.exitCode = 1
			case "changed after copy":
				f.changeAfterCopy = func() { f.workstation.Config.Env[0] = "HCORRAL_HOST_UID=1000" }
			case "cancelled":
				f.onStart = cancel
				f.startErr = context.Canceled
			case "changed helper owner":
				f.startErr = failure
				f.badCleanupOwner = true
			}
			if err := d.Run(ctx, workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("lost transfer failure")
			}
			if (f.helper != nil) != (problem == "changed helper owner") {
				t.Fatalf("incorrect helper cleanup: %+v", f.helper)
			}
			if !f.workstation.State.Running {
				t.Fatal("failure stopped workstation")
			}
		})
	}
}

func TestTargetRejectsAmbiguousOrUnsupportedStorage(t *testing.T) {
	for _, problem := range []string{"absent", "uid", "groups", "home", "codex home", "paused", "restarting", "dead", "unmounted", "readonly", "tmpfs", "duplicate mount"} {
		t.Run(problem, func(t *testing.T) {
			_, f, _, _ := transportFixture(t)
			c := f.workstation
			switch problem {
			case "absent":
				c = nil
			case "uid":
				c.Config.Env[0] = "HCORRAL_HOST_UID=-1"
			case "groups":
				c.Config.Env[2] = "HCORRAL_HOST_GROUPS=bad"
			case "home":
				c.Config.Env[3] = "HCORRAL_CONTAINER_HOME=relative"
			case "codex home":
				c.Config.Env = append(c.Config.Env, "CODEX_HOME=/elsewhere")
			case "paused":
				c.State.Paused = true
			case "restarting":
				c.State.Restarting = true
			case "dead":
				c.State.Status = "dead"
			case "unmounted":
				c.Mounts = c.Mounts[:1]
			case "readonly":
				c.Mounts[1].RW = false
			case "tmpfs":
				c.Mounts[1].Type = "tmpfs"
			case "duplicate mount":
				c.Mounts = append(c.Mounts, c.Mounts[1])
			}
			if _, err := InspectTarget(c); err == nil {
				t.Fatal("accepted invalid target")
			}
		})
	}
}

func TestBindMountCSVPreservesDaemonPathsAndPermissions(t *testing.T) {
	mount := containerruntime.Mount{Type: "bind", Source: `/daemon/a, "quoted" path`, Destination: "/home/actual user/.codex", RW: false}
	got, err := csv.NewReader(strings.NewReader(mountArgument(mount))).Read()
	want := []string{"type=bind", "dst=" + mount.Destination, "src=" + mount.Source, "bind-propagation=rprivate", "readonly=true"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("mount argv round trip: %q %v", got, err)
	}
}

func TestHelperOverridesImageVolumesAndHealthcheck(t *testing.T) {
	d, f, workspace, target := transportFixture(t)
	f.image.Config.Volumes = map[string]struct{}{target.Home: {}, "/unused image data": {}}
	if err := d.Run(context.Background(), workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard); err != nil {
		t.Fatal(err)
	}
	var create []string
	for _, args := range f.commands {
		if args[1] == "create" {
			create = args
		}
	}
	if !strings.Contains(strings.Join(create, "\n"), "--no-healthcheck") {
		t.Fatal("image healthcheck can run")
	}
	var mounts []string
	for i, value := range create {
		if value == "--mount" {
			mounts = append(mounts, create[i+1])
		}
	}
	if len(mounts) != 2 || !strings.Contains(mounts[1], "type=tmpfs,dst=/unused image data,tmpfs-size=1048576") {
		t.Fatalf("image volumes were not covered without anonymous storage: %q", mounts)
	}
}

func TestImageVolumesCannotHideHelperOrConversation(t *testing.T) {
	for _, volume := range []string{"/", helperPath, helperPath + "/child", "/home/actual user/.codex/sessions", "relative"} {
		t.Run(volume, func(t *testing.T) {
			d, f, workspace, target := transportFixture(t)
			f.image.Config.Volumes = map[string]struct{}{volume: {}}
			if err := d.Run(context.Background(), workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard); err == nil {
				t.Fatal("accepted shadowing image volume")
			}
			for _, args := range f.commands {
				if args[1] == "create" {
					t.Fatal("created helper with shadowed storage")
				}
			}
		})
	}
}

func TestRelocatedSQLiteUsesNarrowReadOnlyPersistentStorage(t *testing.T) {
	for _, kind := range []string{"bind", "volume", "already selected", "not persistent"} {
		t.Run(kind, func(t *testing.T) {
			_, f, _, _ := transportFixture(t)
			database := "/workspace/state"
			switch kind {
			case "volume":
				f.workstation.Mounts[0].Type, f.workstation.Mounts[0].Name = "volume", "workspace-data"
				definition := containerruntime.MountDefinition{Type: "volume", Source: "workspace-data", Target: "/workspace"}
				definition.VolumeOptions.Subpath = "existing/subpath"
				f.workstation.HostConfig.Mounts = []containerruntime.MountDefinition{definition}
			case "already selected":
				database = "/home/actual user"
			case "not persistent":
				database = "/image-only-database"
			}
			f.workstation.Mounts = append(f.workstation.Mounts,
				containerruntime.Mount{Type: "bind", Source: "/daemon/database-wal", Destination: database + "/state_5.sqlite-wal", RW: true},
				containerruntime.Mount{Type: "bind", Source: "/daemon/unrelated", Destination: database + "/unrelated", RW: true})
			target, err := InspectTargetWithSQLite(f.workstation, database)
			if kind == "not persistent" {
				if err == nil {
					t.Fatal("accepted database in image/rootfs")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if target.SQLiteHome != database {
				t.Fatal("lost effective database path")
			}
			var selected bool
			for _, mount := range target.Mounts {
				if mount.Source == "/daemon/unrelated" || mount.Source == "/daemon/home/git" {
					t.Fatal("mounted unrelated data for SQLite")
				}
				if mount.Destination == "/home/actual user" {
					if !mount.RW {
						t.Fatal("made writer lock namespace read-only")
					}
					continue
				}
				if mount.RW {
					t.Fatal("relocated SQLite mount is writable")
				}
				if mount.Destination == database {
					selected = true
					if kind == "bind" && mount.Source != "/daemon/workspace/state" {
						t.Fatal("mounted whole workspace instead of metadata directory")
					}
					if kind == "volume" && (mount.Name != "workspace-data" || mount.Subpath != "existing/subpath/state") {
						t.Fatal("lost deployed subpath while narrowing metadata mount")
					}
				}
			}
			if !selected && kind != "already selected" {
				t.Fatal("missing relocated metadata mount")
			}
		})
	}
}

func TestRelocatedSQLiteWritesRespectDirectionAndDeployedPermissions(t *testing.T) {
	for _, operation := range []string{"export", "import"} {
		for _, writable := range []bool{false, true} {
			t.Run(operation+map[bool]string{true: "/rw", false: "/ro"}[writable], func(t *testing.T) {
				d, f, workspace, _ := transportFixture(t)
				f.workstation.Mounts[0].RW = writable
				database := "/workspace/state"
				f.workstation.Mounts = append(f.workstation.Mounts, containerruntime.Mount{Type: "bind", Source: "/daemon/wal", Destination: database + "/state_5.sqlite-wal", RW: false})
				target, err := InspectTargetForTransfer(f.workstation, database, operation)
				if err != nil {
					t.Fatal(err)
				}
				var found bool
				for _, mount := range target.Mounts {
					if mount.Destination == database {
						found = true
						if mount.Source != "/daemon/workspace/state" || mount.RW != (writable && operation == "import") {
							t.Fatalf("wrong metadata access: %+v", mount)
						}
					}
					if mount.Destination == database+"/state_5.sqlite-wal" && mount.RW {
						t.Fatal("broadened deployed read-only sidecar access")
					}
				}
				if !found {
					t.Fatal("missing relocated metadata")
				}
				if err := d.recheck(context.Background(), workspace, target); err != nil {
					t.Fatal(err)
				}
				if operation == "import" {
					f.workstation.Mounts[0].RW = !writable
					if err := d.recheck(context.Background(), workspace, target); err == nil {
						t.Fatal("metadata permissions changed after inspection without detection")
					}
				}
			})
		}
	}
}

func TestDeployedVolumeSubpathIsPreservedAndValidated(t *testing.T) {
	for _, subpath := range []string{"users/alice", ".", "../escape", "/absolute", "users/../other"} {
		t.Run(subpath, func(t *testing.T) {
			d, f, workspace, _ := transportFixture(t)
			definition := containerruntime.MountDefinition{Type: "volume", Source: "existing-state", Target: "/home/actual user"}
			definition.VolumeOptions.Subpath = subpath
			f.workstation.HostConfig.Mounts = []containerruntime.MountDefinition{definition}
			target, err := InspectTarget(f.workstation)
			if subpath != "users/alice" && subpath != "." {
				if err == nil {
					t.Fatal("accepted invalid volume subpath")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Run(context.Background(), workspace, target, []string{"protocol"}, nil, io.Discard, io.Discard); err != nil {
				t.Fatal(err)
			}
			for _, args := range f.commands {
				if args[1] == "create" && !strings.Contains(argValue(args, "--mount"), "volume-subpath="+subpath) {
					t.Fatal("mounted volume root instead of deployed subpath")
				}
			}
			f.workstation.HostConfig.Mounts[0].Source = "another-volume"
			if _, err := InspectTarget(f.workstation); err == nil {
				t.Fatal("accepted inconsistent volume definition")
			}
			f.workstation.HostConfig.Mounts = append(f.workstation.HostConfig.Mounts, definition)
			if _, err := InspectTarget(f.workstation); err == nil {
				t.Fatal("accepted duplicate volume definition")
			}
		})
	}
}
