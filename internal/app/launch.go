package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/gui"
	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/legacyguard"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/update"
)

func runOperational(cfg config.Config, workspace identity.Workspace, streams Streams, runner command.Runner) int {
	ctx := context.Background()
	docker := containerruntime.NewDocker(runner).WithStreams(streams.Out, streams.Err)
	containers, dockerErr := docker.ListContainers(ctx)
	if dockerErr == nil {
		warnCorralMultiplicity(streams.Err, containers, workspace)
	}
	legacy := (*legacyguard.Match)(nil)
	if dockerErr == nil {
		legacy = legacyguard.Find(containers, workspace.Path)
	}
	candidate := findContainer(containers, workspace.Project)
	var ownershipErr error
	if candidate != nil {
		ownershipErr = identity.VerifyContainer(candidate, workspace)
		if ownershipErr == nil && !cfg.StateSpecified {
			adoptDeployedState(&cfg, candidate, workspace)
		}
	}
	if ownershipErr == nil {
		ownershipErr = verifyProjectContainers(containers, workspace, candidate)
	}

	name := commandName(cfg.Command)
	if name == "info" {
		return printSnapshot(ctx, streams, cfg, workspace, candidate, containers, legacy, dockerErr, ownershipErr, runner, docker)
	}
	if ownershipErr != nil {
		return fail(streams.Err, 1, "%v", ownershipErr)
	}
	if dockerErr != nil {
		return fail(streams.Err, 1, "%v", dockerErr)
	}
	if legacy != nil {
		return refuseLegacy(streams.Err, legacy)
	}
	if candidate != nil && cfg.Platform == "darwin" && deployedGUI(candidate) != "none" {
		return fail(streams.Err, 2, "container uses unsupported GUI mode %s on macOS; hcorral will not start, attach, or reconcile it", deployedGUI(candidate))
	}

	if len(cfg.Command) == 0 {
		return runDefault(ctx, cfg, workspace, candidate, containers, streams, runner, docker)
	}
	if name == "attach" {
		return attachExisting(ctx, cfg, workspace, candidate, streams, runner, true)
	}
	return runCommand(ctx, cfg, workspace, candidate, containers, streams, runner, docker)
}

func runDefault(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, streams Streams, runner command.Runner, docker containerruntime.Docker) int {
	if candidate != nil && candidate.State.Running {
		if err := guardAttachGUI(ctx, cfg, workspace, candidate, runner); err != nil {
			return fail(streams.Err, 2, "%v", err)
		}
		reportDrift(ctx, cfg, workspace, candidate, containers, streams, runner)
		if !sessionReady(ctx, docker, cfg, workspace.Project) {
			recovered, legacy, recoveryErr := recoverAttachSessionLocked(ctx, cfg, workspace, streams, runner, docker)
			if legacy != nil {
				return refuseLegacy(streams.Err, legacy)
			}
			if recoveryErr != nil {
				return fail(streams.Err, 1, "%v", recoveryErr)
			}
			candidate = recovered
		}
		update.Checker{Docker: docker, Out: streams.Err, LauncherVersion: Version}.Notify(ctx, cfg, candidate)
		return replaceAttach(cfg, workspace.Project, streams, runner)
	}

	lock, err := identity.AcquireLock(workspace.Project)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	defer lock.Close()
	containers, err = docker.ListContainers(ctx)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	candidate = findContainer(containers, workspace.Project)
	if err := identity.VerifyContainer(candidate, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := verifyProjectContainers(containers, workspace, candidate); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if legacy := legacyguard.Find(containers, workspace.Path); legacy != nil {
		return refuseLegacy(streams.Err, legacy)
	}
	if candidate != nil && !cfg.StateSpecified {
		adoptDeployedState(&cfg, candidate, workspace)
	}
	if candidate != nil && cfg.Platform == "darwin" && deployedGUI(candidate) != "none" {
		return fail(streams.Err, 2, "container uses unsupported GUI mode %s on macOS; hcorral will not start, attach, or reconcile it", deployedGUI(candidate))
	}
	if candidate != nil {
		if drift, detail := desiredDrift(ctx, cfg, workspace, containers, runner, candidate); drift != "none" {
			return fail(streams.Err, 1, "stopped environment has %s drift (%s); run `hcorral up -d` explicitly", drift, detail)
		}
		if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if err := docker.StartContainer(ctx, workspace.Project); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		_ = lock.Close()
		if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		notifyRunningContainer(ctx, cfg, workspace.Project, streams.Err, docker)
		return replaceAttach(cfg, workspace.Project, streams, runner)
	}

	_, _, generated, project, err := prepareProject(ctx, cfg, workspace, nil, streams, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	rendered, err := project.Render(ctx)
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if err := validateComposeNetworkOwnership(ctx, docker, rendered, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := validateComposeVolumeOwnership(ctx, docker, rendered, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := ensureSelectedImage(ctx, docker, cfg, streams); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := ensureState(ctx, docker, cfg, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := project.Run(ctx, "up", "-d", "--no-build", "--pull", "never", "hcorral"); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	_ = lock.Close()
	if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	notifyRunningContainer(ctx, cfg, workspace.Project, streams.Err, docker)
	return replaceAttach(cfg, workspace.Project, streams, runner)
}

func notifyRunningContainer(ctx context.Context, cfg config.Config, containerName string, out interface{ Write([]byte) (int, error) }, docker containerruntime.Docker) {
	container, err := docker.InspectContainer(ctx, containerName)
	if err != nil || container == nil {
		return
	}
	update.Checker{Docker: docker, Out: out, LauncherVersion: Version}.Notify(ctx, cfg, container)
}

func runCommand(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, streams Streams, runner command.Runner, docker containerruntime.Docker) int {
	name, args := cfg.Command[0], cfg.Command[1:]
	if name == "state" {
		return runStateCommand(ctx, args, workspace, containers, streams, docker)
	}
	if name == "exec" && len(args) == 0 {
		return fail(streams.Err, 2, "exec requires a command")
	}
	var lock *identity.Lock
	if composeCommandMutates(name) {
		var err error
		lock, err = identity.AcquireLock(workspace.Project)
		if err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		defer lock.Close()
		containers, err = docker.ListContainers(ctx)
		if err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		candidate = findContainer(containers, workspace.Project)
		if err := identity.VerifyContainer(candidate, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if err := verifyProjectContainers(containers, workspace, candidate); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if legacy := legacyguard.Find(containers, workspace.Path); legacy != nil {
			return refuseLegacy(streams.Err, legacy)
		}
		if candidate != nil && !cfg.StateSpecified {
			adoptDeployedState(&cfg, candidate, workspace)
		}
		if candidate != nil && cfg.Platform == "darwin" && deployedGUI(candidate) != "none" {
			return fail(streams.Err, 2, "container uses unsupported GUI mode %s on macOS; hcorral will not start, attach, or reconcile it", deployedGUI(candidate))
		}
	}

	switch name {
	case "attach":
		return attachExisting(ctx, cfg, workspace, candidate, streams, runner, true)
	case "exec":
		if candidate == nil || !candidate.State.Running {
			return fail(streams.Err, 1, "hcorral container is not running")
		}
		_ = lockClose(lock)
		return replaceExec(cfg, workspace.Project, args, streams, runner)
	case "pull":
		if len(args) == 0 {
			if err := docker.PullImage(ctx, cfg.Image, streams.Out, streams.Err); err != nil {
				return fail(streams.Err, childExitCode(err), "%v", err)
			}
			return 0
		}
	case "start":
		if candidate == nil {
			return fail(streams.Err, 1, "hcorral environment does not exist")
		}
		if len(args) == 0 {
			if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
			if err := docker.StartContainer(ctx, workspace.Project); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
			return 0
		}
	}
	if (name == "stop" || name == "restart") && len(args) == 0 {
		args = []string{"hcorral"}
	}

	_, _, generated, project, err := prepareProject(ctx, cfg, workspace, candidate, streams, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	var rendered compose.Rendered
	if composeCommandMutates(name) {
		rendered, err = project.Render(ctx)
		if err != nil {
			return fail(streams.Err, 2, "%v", err)
		}
		if err := validateComposeNetworkOwnership(ctx, docker, rendered, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if err := validateComposeVolumeOwnership(ctx, docker, rendered, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
	}
	if name == "up" || name == "create" {
		if candidate != nil && deployedGUI(candidate) != "none" && !cfg.GUI.Specified {
			return fail(streams.Err, 2, "container uses GUI mode %s; repeat --gui=%s to preserve it or use --no-gui", deployedGUI(candidate), deployedGUI(candidate))
		}
		if err := validateStateOwnership(ctx, docker, cfg, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if !hasBuildOrPull(args) {
			if err := ensureSelectedImage(ctx, docker, cfg, streams); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		if err := ensureState(ctx, docker, cfg, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		args = safeReconcileArgs(args)
	}
	if name == "down" {
		removeVolumes := hasAny(args, "-v", "--volumes")
		removeState := false
		removeName := stateVolumeName(cfg, workspace)
		var stateLock *identity.Lock
		if removeVolumes && cfg.StateMode == config.StatePrivate {
			stateLock, err = identity.AcquireVolumeLock(stateVolumeName(cfg, workspace))
			if err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
			defer stateLock.Close()
		}
		if removeVolumes {
			currentContainers, listErr := docker.ListContainers(ctx)
			if listErr != nil {
				return fail(streams.Err, 1, "%v", listErr)
			}
			removeState, removeName, err = planManagedStateRemoval(ctx, docker, cfg, workspace, currentContainers)
			if err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		fmt.Fprintf(streams.Err, "hcorral: removal plan: Compose project %s", workspace.Project)
		composeVolumes, composeNetworks, retainedExternalVolumes, retainedExternalNetworks := renderedRemovalTargets(rendered, removeName)
		for _, name := range composeNetworks {
			fmt.Fprintf(streams.Err, "; Compose network %s", name)
		}
		if removeVolumes {
			for _, name := range composeVolumes {
				fmt.Fprintf(streams.Err, "; Compose volume %s", name)
			}
		}
		if removeVolumes && removeState {
			fmt.Fprintf(streams.Err, "; then Docker volume %s", removeName)
		} else if removeVolumes {
			fmt.Fprintf(streams.Err, "; retain external volume %s", removeName)
		}
		for _, name := range retainedExternalVolumes {
			fmt.Fprintf(streams.Err, "; retain external volume %s", name)
		}
		for _, name := range retainedExternalNetworks {
			fmt.Fprintf(streams.Err, "; retain external network %s", name)
		}
		fmt.Fprintln(streams.Err)
		if err := project.Run(ctx, append([]string{"down"}, args...)...); err != nil {
			return fail(streams.Err, childExitCode(err), "%v", err)
		}
		if removeVolumes && removeState {
			if err := docker.RemoveVolume(ctx, removeName); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		return 0
	}
	if err := project.Run(ctx, append([]string{name}, args...)...); err != nil {
		return fail(streams.Err, childExitCode(err), "%v", err)
	}
	return 0
}

func runStateCommand(ctx context.Context, args []string, workspace identity.Workspace, containers []containerruntime.Container, streams Streams, docker containerruntime.Docker) int {
	if len(args) != 3 || args[0] != "rm" || args[1] != "--scope" || (args[2] != "global" && args[2] != "workspace") {
		return fail(streams.Err, 2, "state accepts only `state rm --scope global|workspace`")
	}
	name := "hcorral_state"
	want := identity.SharedVolumeLabels()
	if args[2] == "workspace" {
		name = identity.WorkspaceVolumeName(workspace)
		want = identity.PrivateVolumeLabels(workspace)
	}
	lock, err := identity.AcquireVolumeLock(name)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	defer lock.Close()
	containers, err = docker.ListContainers(ctx)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	volume, err := docker.InspectVolume(ctx, name)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if volume == nil {
		fmt.Fprintf(streams.Err, "hcorral: volume %s is already absent\n", name)
		return 0
	}
	labelKeys := make([]string, 0, len(want))
	for key := range want {
		labelKeys = append(labelKeys, key)
	}
	sort.Strings(labelKeys)
	fmt.Fprintf(streams.Err, "hcorral: state removal target: %s", name)
	for _, key := range labelKeys {
		fmt.Fprintf(streams.Err, "; %s=%q", key, volume.Labels[key])
	}
	fmt.Fprintln(streams.Err)
	for _, key := range labelKeys {
		value := want[key]
		if volume.Labels[key] != value {
			return fail(streams.Err, 1, "refuse to remove volume %s: ownership label %s does not match", name, key)
		}
	}
	references := []string{}
	for _, container := range containers {
		for _, mount := range container.Mounts {
			if mount.Type == "volume" && mount.Name == name {
				references = append(references, container.CleanName())
			}
		}
	}
	sort.Strings(references)
	if len(references) > 0 {
		fmt.Fprintf(streams.Err, "hcorral: state removal references: %s\n", strings.Join(references, ", "))
		return fail(streams.Err, 1, "refuse to remove volume %s: %d container(s) still reference it", name, len(references))
	}
	fmt.Fprintln(streams.Err, "hcorral: state removal references: none")
	fmt.Fprintf(streams.Err, "hcorral: removing unreferenced %s state volume %s\n", args[2], name)
	if err := docker.RemoveVolume(ctx, name); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	return 0
}

func prepareProject(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, streams Streams, runner command.Runner) (gui.Selection, compose.AssetPaths, compose.GeneratedFile, compose.Project, error) {
	assets, err := (compose.Materializer{}).Materialize()
	if err != nil {
		return gui.Selection{}, assets, compose.GeneratedFile{}, compose.Project{}, err
	}
	selection := gui.Selection{Mode: "none", Env: map[string]string{"HCORRAL_GUI_MODE": "none"}}
	if cfg.GUI.Specified {
		selection, err = gui.NewResolver(runner).Resolve(ctx, cfg.GUI, workspace, assets)
	} else if candidate != nil {
		selection, err = selectionFromContainer(candidate, assets)
	}
	if err != nil {
		return selection, assets, compose.GeneratedFile{}, compose.Project{}, err
	}
	generated, err := compose.ExtraMountOverlay(cfg)
	if err != nil {
		return selection, assets, generated, compose.Project{}, err
	}
	invocation, err := compose.NewInvocation(cfg, workspace, assets, selection.File, selection.Env)
	if err != nil {
		return selection, assets, generated, compose.Project{}, err
	}
	if generated.Path != "" {
		invocation.Files = append(invocation.Files, generated.Path)
	}
	return selection, assets, generated, compose.Project{Runner: runner, Invocation: invocation, Out: streams.Out, Err: streams.Err}, nil
}

func selectionFromContainer(container *containerruntime.Container, assets compose.AssetPaths) (gui.Selection, error) {
	mode := deployedGUI(container)
	selection := gui.Selection{Mode: mode, Env: map[string]string{"HCORRAL_GUI_MODE": mode}}
	switch mode {
	case "none":
		return selection, nil
	case "x11":
		selection.File = assets.X11
		selection.Env["HCORRAL_X11_DISPLAY"] = containerEnv(container, "DISPLAY")
		for _, mount := range container.Mounts {
			if mount.Destination == "/tmp/.hcorral-xauthority" {
				selection.Env["HCORRAL_X11_AUTHORITY"] = mount.Source
			}
			if strings.HasPrefix(mount.Destination, "/tmp/.X11-unix/X") {
				selection.Env["HCORRAL_X11_SOCKET"] = mount.Source
			}
		}
	case "wayland":
		selection.File = assets.Wayland
		for _, mount := range container.Mounts {
			if mount.Destination == "/tmp/.hcorral-wayland" {
				selection.Env["HCORRAL_WAYLAND_SOCKET"] = mount.Source
			}
		}
	default:
		return selection, fmt.Errorf("container has unsupported GUI label %q", mode)
	}
	for key, value := range selection.Env {
		if key != "HCORRAL_GUI_MODE" && value == "" {
			return selection, fmt.Errorf("container GUI mode %s lacks required deployed mount/environment evidence", mode)
		}
	}
	return selection, nil
}

func guardAttachGUI(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, runner command.Runner) error {
	if candidate == nil || !candidate.State.Running {
		return errors.New("hcorral container is not running")
	}
	if cfg.Platform == "darwin" && deployedGUI(candidate) != "none" {
		return fmt.Errorf("container uses unsupported GUI mode %s on macOS", deployedGUI(candidate))
	}
	if !cfg.GUI.Specified {
		return nil
	}
	assets, err := (compose.Materializer{}).Materialize()
	if err != nil {
		return err
	}
	selection, err := gui.NewResolver(runner).Resolve(ctx, cfg.GUI, workspace, assets)
	if err != nil {
		return err
	}
	if selection.Mode != deployedGUI(candidate) {
		return fmt.Errorf("container uses GUI mode %s; changing to %s requires `hcorral --gui=%s up -d`", deployedGUI(candidate), selection.Mode, selection.Mode)
	}
	return nil
}

func attachExisting(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, streams Streams, runner command.Runner, recover bool) int {
	if err := guardAttachGUI(ctx, cfg, workspace, candidate, runner); err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if recover {
		docker := containerruntime.NewDocker(runner).WithStreams(streams.Out, streams.Err)
		if !sessionReady(ctx, docker, cfg, workspace.Project) {
			var legacy *legacyguard.Match
			var err error
			candidate, legacy, err = recoverAttachSessionLocked(ctx, cfg, workspace, streams, runner, docker)
			if legacy != nil {
				return refuseLegacy(streams.Err, legacy)
			}
			if err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		update.Checker{Docker: docker, Out: streams.Err, LauncherVersion: Version}.Notify(ctx, cfg, candidate)
	}
	return replaceAttach(cfg, workspace.Project, streams, runner)
}

// recoverAttachSessionLocked serializes the in-place session mutation with all
// other hcorral lifecycle changes. It re-inspects every ownership boundary
// after taking the lock and returns before the caller replaces itself for the
// long-running interactive attach.
func recoverAttachSessionLocked(ctx context.Context, cfg config.Config, workspace identity.Workspace, streams Streams, runner command.Runner, docker containerruntime.Docker) (*containerruntime.Container, *legacyguard.Match, error) {
	lock, err := identity.AcquireLock(workspace.Project)
	if err != nil {
		return nil, nil, err
	}
	defer lock.Close()

	containers, err := docker.ListContainers(ctx)
	if err != nil {
		return nil, nil, err
	}
	candidate := findContainer(containers, workspace.Project)
	if err := identity.VerifyContainer(candidate, workspace); err != nil {
		return nil, nil, err
	}
	if err := verifyProjectContainers(containers, workspace, candidate); err != nil {
		return nil, nil, err
	}
	if legacy := legacyguard.Find(containers, workspace.Path); legacy != nil {
		return nil, legacy, nil
	}
	if candidate == nil || !candidate.State.Running {
		return nil, nil, errors.New("hcorral container is not running")
	}
	if err := guardAttachGUI(ctx, cfg, workspace, candidate, runner); err != nil {
		return nil, nil, err
	}
	if sessionReady(ctx, docker, cfg, workspace.Project) {
		return candidate, nil, nil
	}
	if err := recoverSession(ctx, docker, workspace.Project); err != nil {
		return nil, nil, fmt.Errorf("recover workstation session: %w", err)
	}
	if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
		return nil, nil, err
	}
	return candidate, nil, nil
}

func replaceAttach(cfg config.Config, container string, streams Streams, runner command.Runner) int {
	current, err := user.Current()
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	argv := []string{"docker", "exec", "-it", container, "gosu", current.Uid, "env", "HOME=" + cfg.ContainerHome, "bash", "--login", "-c", `runtime_user="$(id -un)"; export USER="${runtime_user}" LOGNAME="${runtime_user}"; exec tmux attach -t "$1"`, "bash", cfg.Session}
	if err := runner.Replace(argv, command.EnvironmentWithoutCompose(os.Environ())); err != nil {
		return fail(streams.Err, 1, "attach: %v", err)
	}
	return 0
}

func replaceExec(cfg config.Config, container string, args []string, streams Streams, runner command.Runner) int {
	current, err := user.Current()
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	execMode := "-i"
	if isTerminal(streams.In) && isTerminal(streams.Out) {
		execMode = "-it"
	}
	argv := []string{"docker", "exec", execMode, container, "gosu", current.Uid, "env", "HOME=" + cfg.ContainerHome, "bash", "--login", "-c", `runtime_user="$(id -un)"; export USER="${runtime_user}" LOGNAME="${runtime_user}"; cd "$1"; shift; exec "$@"`, "bash", cfg.Workdir}
	argv = append(argv, args...)
	if err := runner.Replace(argv, command.EnvironmentWithoutCompose(os.Environ())); err != nil {
		return fail(streams.Err, 1, "exec: %v", err)
	}
	return 0
}

func sessionReady(ctx context.Context, docker containerruntime.Docker, cfg config.Config, container string) bool {
	current, err := user.Current()
	if err != nil {
		return false
	}
	_, err = docker.ExecCapture(ctx, container, "gosu", current.Uid, "env", "HOME="+cfg.ContainerHome, "byobu-tmux", "has-session", "-t", cfg.Session)
	return err == nil
}

func recoverSession(ctx context.Context, docker containerruntime.Docker, container string) error {
	_, err := docker.ExecCapture(ctx, container, "/usr/local/bin/hcorral-session-init")
	return err
}

func waitReady(ctx context.Context, docker containerruntime.Docker, cfg config.Config, container string, streams Streams) error {
	deadline := time.Now().Add(time.Duration(cfg.WaitTimeoutSeconds) * time.Second)
	next := time.Now()
	for time.Now().Before(deadline) {
		inspected, err := docker.InspectContainer(ctx, container)
		if err != nil {
			return err
		}
		if inspected != nil && inspected.State.Running && sessionReady(ctx, docker, cfg, container) {
			return nil
		}
		if inspected != nil && (inspected.State.Status == "exited" || inspected.State.Status == "dead") {
			reportStartupLogs(ctx, docker, container, streams)
			return fmt.Errorf("startup readiness phase failed: container became %s", inspected.State.Status)
		}
		if time.Now().After(next) {
			fmt.Fprintf(streams.Err, "hcorral: waiting for workstation session (%s)\n", stateOf(inspected))
			next = time.Now().Add(time.Duration(cfg.ProgressIntervalSecond) * time.Second)
		}
		time.Sleep(250 * time.Millisecond)
	}
	reportStartupLogs(ctx, docker, container, streams)
	return fmt.Errorf("startup readiness phase timed out after %ds", cfg.WaitTimeoutSeconds)
}

var (
	ansiLogEscape     = regexp.MustCompile("\\x1b\\[[0-?]*[ -/]*[@-~]")
	sensitiveLogValue = regexp.MustCompile(`(?i)\b(token|password|secret|authorization|api[_-]?key)=([^[:space:]]+)`)
)

func reportStartupLogs(ctx context.Context, docker containerruntime.Docker, container string, streams Streams) {
	result, err := docker.ContainerLogs(ctx, container, 80)
	if err != nil {
		fmt.Fprintln(streams.Err, "hcorral: startup logs unavailable")
		return
	}
	lines := sanitizedLogLines(append(append([]byte(nil), result.Stdout...), result.Stderr...), 80)
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(streams.Err, "hcorral: last container log lines (sanitized; maximum 80):")
	for _, line := range lines {
		fmt.Fprintf(streams.Err, "hcorral: log: %s\n", line)
	}
}

func sanitizedLogLines(content []byte, limit int) []string {
	if limit <= 0 {
		return nil
	}
	raw := strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
	if len(raw) > 0 && raw[len(raw)-1] == "" {
		raw = raw[:len(raw)-1]
	}
	if len(raw) > limit {
		raw = raw[len(raw)-limit:]
	}
	result := make([]string, 0, len(raw))
	for _, line := range raw {
		line = ansiLogEscape.ReplaceAllString(line, "")
		var cleaned strings.Builder
		for _, r := range line {
			if r == '\t' || (unicode.IsPrint(r) && r != '\x1b') {
				cleaned.WriteRune(r)
			}
		}
		value := sensitiveLogValue.ReplaceAllString(cleaned.String(), "$1=<redacted>")
		if len(value) > 1024 {
			value = value[:1024] + "…"
		}
		result = append(result, value)
	}
	return result
}

func ensureSelectedImage(ctx context.Context, docker containerruntime.Docker, cfg config.Config, streams Streams) error {
	reference := cfg.Image
	image, err := docker.InspectImage(ctx, reference)
	if err != nil {
		return err
	}
	if image != nil {
		return nil
	}
	fmt.Fprintf(streams.Err, "hcorral: pulling missing image %s\n", reference)
	if err := docker.PullImage(ctx, reference, streams.Out, streams.Err); err != nil {
		return err
	}
	image, err = docker.InspectImage(ctx, reference)
	if err != nil {
		return err
	}
	if image == nil {
		return fmt.Errorf("pull completed but image remains unavailable: %s", reference)
	}
	return nil
}

func desiredDrift(ctx context.Context, cfg config.Config, workspace identity.Workspace, containers []containerruntime.Container, runner command.Runner, candidate *containerruntime.Container) (string, string) {
	_, _, generated, project, err := prepareProject(ctx, cfg, workspace, candidate, Streams{Out: ioDiscard{}, Err: ioDiscard{}}, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		return "unknown", err.Error()
	}
	rendered, err := project.Render(ctx)
	if err != nil {
		return "unknown", err.Error()
	}
	if len(rendered.Hashes) == 0 {
		return "unknown", "Compose config hash unavailable"
	}
	deployed := map[string]string{}
	for _, container := range containers {
		if container.Config.Labels["com.docker.compose.project"] == workspace.Project {
			deployed[container.Config.Labels["com.docker.compose.service"]] = container.Config.Labels["com.docker.compose.config-hash"]
		}
	}
	if len(deployed) != len(rendered.Services) {
		return "present", "service set differs"
	}
	for service := range rendered.Services {
		if rendered.Hashes[service] == "" || deployed[service] != rendered.Hashes[service] {
			return "present", "service " + service + " hash differs"
		}
	}
	return "none", ""
}

func reportDrift(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, streams Streams, runner command.Runner) {
	drift, detail := desiredDrift(ctx, cfg, workspace, containers, runner, candidate)
	if drift != "none" {
		fmt.Fprintf(streams.Err, "hcorral: desired/deployed Compose drift is %s (%s); attaching without reconciliation\n", drift, detail)
	}
}

func findContainer(containers []containerruntime.Container, name string) *containerruntime.Container {
	for index := range containers {
		if containers[index].CleanName() == name {
			return &containers[index]
		}
	}
	return nil
}

func verifyProjectContainers(containers []containerruntime.Container, workspace identity.Workspace, primary *containerruntime.Container) error {
	members := 0
	for index := range containers {
		container := &containers[index]
		if container.Config.Labels["com.docker.compose.project"] != workspace.Project {
			continue
		}
		members++
		service := container.Config.Labels["com.docker.compose.service"]
		if service == "" || container.Config.Labels["com.docker.compose.config-hash"] == "" {
			return fmt.Errorf("container %s has incomplete Compose ownership evidence for project %s", container.CleanName(), workspace.Project)
		}
		if owner := container.Config.Labels[identity.LabelWorkspaceID]; owner != "" && owner != workspace.FullID {
			return fmt.Errorf("container %s carries conflicting hcorral workspace ID %s", container.CleanName(), owner)
		}
		if owner := container.Config.Labels[identity.LabelCorralID]; owner != "" && owner != workspace.CorralID {
			return fmt.Errorf("container %s carries conflicting hcorral corral ID %s", container.CleanName(), owner)
		}
		if service == "hcorral" && (primary == nil || container.CleanName() != workspace.Project || container.ID != primary.ID) {
			return fmt.Errorf("project %s has an ambiguous primary hcorral service container %s", workspace.Project, container.CleanName())
		}
	}
	if members > 0 && primary == nil {
		return fmt.Errorf("project %s has Compose container residue but no verified primary hcorral container", workspace.Project)
	}
	return nil
}

func refuseLegacy(stderr interface{ Write([]byte) (int, error) }, legacy *legacyguard.Match) int {
	fmt.Fprintf(stderr, "hcorral: this workspace already has a myCodex environment in container %s (%s).\n", legacy.Container, legacy.State)
	fmt.Fprintln(stderr, "hcorral: use the original myCodex launcher to attach, or run `myCodex down` from the original workspace before starting hcorral.")
	fmt.Fprintln(stderr, "hcorral: do not use `down -v` unless you intend to delete its persisted home.")
	return 3
}

func deployedGUI(container *containerruntime.Container) string {
	if container == nil {
		return "none"
	}
	mode := container.Config.Labels[identity.LabelGUI]
	if mode == "" {
		return "none"
	}
	return mode
}
func containerEnv(container *containerruntime.Container, key string) string {
	prefix := key + "="
	for _, item := range container.Config.Env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}

func adoptDeployedState(cfg *config.Config, container *containerruntime.Container, workspace identity.Workspace) {
	for _, mount := range container.Mounts {
		if mount.Type != "volume" || mount.Destination != cfg.ContainerHome {
			continue
		}
		switch mount.Name {
		case identity.WorkspaceVolumeName(workspace):
			cfg.StateMode, cfg.StateVolumeName = config.StatePrivate, ""
		case "hcorral_state":
			cfg.StateMode, cfg.StateVolumeName = config.StateShared, ""
		default:
			cfg.StateMode, cfg.StateVolumeName = config.StateCustom, mount.Name
		}
		if cfg.Sources != nil {
			cfg.Sources["state"] = "deployed"
		}
		return
	}
}

func warnCorralMultiplicity(stderr interface{ Write([]byte) (int, error) }, containers []containerruntime.Container, workspace identity.Workspace) {
	projects := map[string]bool{}
	for _, container := range containers {
		if container.Config.Labels[identity.LabelCorralID] == workspace.CorralID {
			if project := container.Config.Labels["com.docker.compose.project"]; project != "" && project != workspace.Project {
				projects[project] = true
			}
		}
	}
	if len(projects) == 0 {
		return
	}
	names := make([]string, 0, len(projects))
	for name := range projects {
		names = append(names, name)
	}
	sort.Strings(names)
	fmt.Fprintf(stderr, "hcorral: warning: other projects share corral %s: %s; this command targets only %s\n", workspace.CorralID, strings.Join(names, ", "), workspace.Project)
}
func stateOf(container *containerruntime.Container) string {
	if container == nil {
		return "absent"
	}
	return container.State.Status
}
func hasAny(args []string, values ...string) bool {
	for _, arg := range args {
		for _, value := range values {
			if arg == value {
				return true
			}
		}
	}
	return false
}
func hasBuildOrPull(args []string) bool {
	for _, arg := range args {
		if arg == "--build" || arg == "--pull" || strings.HasPrefix(arg, "--pull=") {
			return true
		}
	}
	return false
}
func safeReconcileArgs(args []string) []string {
	result := append([]string(nil), args...)
	if !hasAny(result, "--build", "--no-build") {
		result = append([]string{"--no-build"}, result...)
	}
	if !hasBuildOrPull(result) {
		result = append([]string{"--pull", "never"}, result...)
	}
	return result
}

func composeCommandMutates(name string) bool {
	switch name {
	case "info", "ps", "config", "images", "logs", "top", "events", "version", "help", "attach", "exec":
		return false
	default:
		return true
	}
}
func lockClose(lock *identity.Lock) error {
	if lock == nil {
		return nil
	}
	return lock.Close()
}
func valueOrEmpty(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

type ioDiscard struct{}

func (ioDiscard) Write(value []byte) (int, error) { return len(value), nil }

func isTerminal(stream any) bool {
	file, ok := stream.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
