package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/user"
	"strings"
	"time"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/gui"
	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/legacyguard"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/update"
)

type snapshot struct {
	Schema           int                `json:"schema"`
	LauncherVersion  string             `json:"launcher_version"`
	LauncherCommit   string             `json:"launcher_commit"`
	Workspace        identity.Workspace `json:"workspace"`
	Project          string             `json:"project"`
	Service          string             `json:"service"`
	Container        string             `json:"container"`
	Image            string             `json:"image"`
	StateMode        config.StateMode   `json:"state_mode"`
	StateVolume      string             `json:"state_volume"`
	GUIRequested     config.GUIIntent   `json:"gui_requested"`
	GUIDeployed      string             `json:"gui_deployed,omitempty"`
	ContainerState   string             `json:"container_state,omitempty"`
	ContainerID      string             `json:"container_id,omitempty"`
	ContainerStarted string             `json:"container_started,omitempty"`
	LegacyConflict   *legacyguard.Match `json:"mycodex_conflict,omitempty"`
	ComposeCommand   map[string]any     `json:"compose_command"`
	DockerError      string             `json:"docker_error,omitempty"`
}

func runOperational(cfg config.Config, workspace identity.Workspace, streams Streams, runner command.Runner) int {
	ctx := context.Background()
	docker := containerruntime.NewDocker(runner).WithStreams(streams.Out, streams.Err)
	containers, dockerErr := docker.ListContainers(ctx)
	legacy := (*legacyguard.Match)(nil)
	if dockerErr == nil {
		legacy = legacyguard.Find(containers, workspace.Path)
	}
	candidate := findContainer(containers, workspace.Project)
	if candidate != nil {
		if err := identity.VerifyContainer(candidate, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if !cfg.StateSpecified {
			adoptDeployedState(&cfg, candidate, workspace)
		}
	}

	name := commandName(cfg.Command)
	if name == "info" {
		return printSnapshot(streams, cfg, workspace, candidate, legacy, dockerErr)
	}
	if dockerErr != nil {
		return fail(streams.Err, 1, "%v", dockerErr)
	}
	if legacy != nil {
		fmt.Fprintf(streams.Err, "hcorral: this workspace already has a myCodex environment in container %s (%s).\n", legacy.Container, legacy.State)
		fmt.Fprintln(streams.Err, "hcorral: use the original myCodex launcher to attach, or run `myCodex down` from the original workspace before starting hcorral.")
		fmt.Fprintln(streams.Err, "hcorral: do not use `down -v` unless you intend to delete its persisted home.")
		return 3
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
			if err := recoverSession(ctx, docker, workspace.Project); err != nil {
				return fail(streams.Err, 1, "recover workstation session: %v", err)
			}
			if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		update.Checker{Docker: docker, Out: streams.Err}.Notify(ctx, cfg, candidate)
		return replaceAttach(cfg, workspace.Project, streams, runner)
	}

	lock, err := identity.AcquireLock(workspace.Project)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	defer lock.Close()
	candidate, err = docker.InspectContainer(ctx, workspace.Project)
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := identity.VerifyContainer(candidate, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if candidate != nil {
		if drift, detail := desiredDrift(ctx, cfg, workspace, containers, runner, candidate); drift != "none" {
			return fail(streams.Err, 1, "stopped environment has %s drift (%s); run `hcorral up -d` explicitly", drift, detail)
		}
		if err := docker.StartContainer(ctx, workspace.Project); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		_ = lock.Close()
		return replaceAttach(cfg, workspace.Project, streams, runner)
	}

	selection, assets, generated, project, err := prepareProject(ctx, cfg, workspace, nil, streams, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	_ = assets
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if err := ensureSelectedImage(ctx, docker, cfg, streams); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := ensureState(ctx, docker, cfg, workspace); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if _, err := project.RenderAndValidate(ctx, cfg, workspace, selection.Mode, stateVolumeName(cfg, workspace)); err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if err := project.Run(ctx, "up", "-d", "--no-build", "--pull", "never", "hcorral"); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	if err := waitReady(ctx, docker, cfg, workspace.Project, streams); err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	_ = lock.Close()
	return replaceAttach(cfg, workspace.Project, streams, runner)
}

func runCommand(ctx context.Context, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, containers []containerruntime.Container, streams Streams, runner command.Runner, docker containerruntime.Docker) int {
	name, args := cfg.Command[0], cfg.Command[1:]
	if name == "exec" && len(args) == 0 {
		return fail(streams.Err, 2, "exec requires a command")
	}
	mutating := map[string]bool{"up": true, "create": true, "down": true, "start": true, "stop": true, "restart": true}
	var lock *identity.Lock
	if mutating[name] {
		var err error
		lock, err = identity.AcquireLock(workspace.Project)
		if err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		defer lock.Close()
		candidate, err = docker.InspectContainer(ctx, workspace.Project)
		if err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if err := identity.VerifyContainer(candidate, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
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
			if err := docker.PullImage(ctx, cfg.ImageName+":"+cfg.ImageTag, streams.Out, streams.Err); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
			return 0
		}
	case "start":
		if candidate == nil {
			return fail(streams.Err, 1, "hcorral environment does not exist")
		}
		if len(args) == 0 {
			if err := docker.StartContainer(ctx, workspace.Project); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
			return 0
		}
	}
	if (name == "stop" || name == "restart") && len(args) == 0 {
		args = []string{"hcorral"}
	}

	selection, _, generated, project, err := prepareProject(ctx, cfg, workspace, candidate, streams, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if name == "up" || name == "create" {
		if candidate != nil && deployedGUI(candidate) != "none" && !cfg.GUI.Specified {
			return fail(streams.Err, 2, "container uses GUI mode %s; repeat --gui=%s to preserve it or use --no-gui", deployedGUI(candidate), deployedGUI(candidate))
		}
		if !hasBuildOrPull(args) {
			if err := ensureSelectedImage(ctx, docker, cfg, streams); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		if err := ensureState(ctx, docker, cfg, workspace); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if _, err := project.RenderAndValidate(ctx, cfg, workspace, selection.Mode, stateVolumeName(cfg, workspace)); err != nil {
			return fail(streams.Err, 2, "%v", err)
		}
		args = safeReconcileArgs(args)
	}
	if name == "down" {
		removeVolumes := hasAny(args, "-v", "--volumes")
		removeState := false
		removeName := stateVolumeName(cfg, workspace)
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
		if removeVolumes && removeState {
			fmt.Fprintf(streams.Err, "; then Docker volume %s\n", removeName)
		} else if removeVolumes {
			fmt.Fprintf(streams.Err, "; retain external volume %s\n", removeName)
		} else {
			fmt.Fprintln(streams.Err)
		}
		if err := project.Run(ctx, append([]string{"down"}, args...)...); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		if removeVolumes && removeState {
			if err := docker.RemoveVolume(ctx, removeName); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		return 0
	}
	if err := project.Run(ctx, append([]string{name}, args...)...); err != nil {
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
		return fail(streams.Err, 1, "%v", err)
	}
	if recover {
		docker := containerruntime.NewDocker(runner).WithStreams(streams.Out, streams.Err)
		if !sessionReady(ctx, docker, cfg, workspace.Project) {
			if err := recoverSession(ctx, docker, workspace.Project); err != nil {
				return fail(streams.Err, 1, "%v", err)
			}
		}
		update.Checker{Docker: docker, Out: streams.Err}.Notify(ctx, cfg, candidate)
	}
	return replaceAttach(cfg, workspace.Project, streams, runner)
}

func replaceAttach(cfg config.Config, container string, streams Streams, runner command.Runner) int {
	current, err := user.Current()
	if err != nil {
		return fail(streams.Err, 1, "%v", err)
	}
	argv := []string{"docker", "exec", "-it", container, "gosu", current.Username, "env", "HOME=" + cfg.ContainerHome, "USER=" + current.Username, "LOGNAME=" + current.Username, "CODEX_HOME=" + cfg.ContainerHome + "/.codex", "tmux", "attach", "-t", cfg.Session}
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
	argv := []string{"docker", "exec", execMode, container, "gosu", current.Username, "env", "HOME=" + cfg.ContainerHome, "USER=" + current.Username, "LOGNAME=" + current.Username, "CODEX_HOME=" + cfg.ContainerHome + "/.codex", "HCORRAL_WORKDIR=" + cfg.Workdir, "bash", "--login", "-c", `cd "${HCORRAL_WORKDIR}"; exec "$@"`, "bash"}
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
	_, err = docker.ExecCapture(ctx, container, "gosu", current.Username, "env", "CODEX_HOME="+cfg.ContainerHome+"/.codex", "byobu-tmux", "has-session", "-t", cfg.Session)
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
			return fmt.Errorf("container became %s during startup", inspected.State.Status)
		}
		if time.Now().After(next) {
			fmt.Fprintf(streams.Err, "hcorral: waiting for workstation session (%s)\n", stateOf(inspected))
			next = time.Now().Add(time.Duration(cfg.ProgressIntervalSecond) * time.Second)
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("startup timed out after %ds", cfg.WaitTimeoutSeconds)
}

func ensureSelectedImage(ctx context.Context, docker containerruntime.Docker, cfg config.Config, streams Streams) error {
	reference := cfg.ImageName + ":" + cfg.ImageTag
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
	selection, _, generated, project, err := prepareProject(ctx, cfg, workspace, candidate, Streams{Out: ioDiscard{}, Err: ioDiscard{}}, runner)
	if generated.Path != "" {
		defer generated.Cleanup()
	}
	if err != nil {
		return "unknown", err.Error()
	}
	rendered, err := project.RenderAndValidate(ctx, cfg, workspace, selection.Mode, stateVolumeName(cfg, workspace))
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

func printSnapshot(streams Streams, cfg config.Config, workspace identity.Workspace, candidate *containerruntime.Container, legacy *legacyguard.Match, dockerErr error) int {
	format := "human"
	if len(cfg.Command) > 1 {
		switch cfg.Command[1] {
		case "--format=json":
			format = "json"
		case "--format=human":
		default:
			return fail(streams.Err, 2, "info accepts only --format=human or --format=json")
		}
	}
	prefix := map[string]any{"executable": "docker", "argument_count": len(cfg.ComposeCommand) - 1}
	if len(cfg.ComposeCommand) > 0 && cfg.ComposeCommand[0] == "docker" && len(cfg.ComposeCommand) == 2 && cfg.ComposeCommand[1] == "compose" {
		prefix = map[string]any{"display": "docker compose"}
	}
	s := snapshot{Schema: 1, LauncherVersion: Version, LauncherCommit: Commit, Workspace: workspace, Project: workspace.Project, Service: "hcorral", Container: workspace.Project, Image: cfg.ImageName + ":" + cfg.ImageTag, StateMode: cfg.StateMode, StateVolume: stateVolumeName(cfg, workspace), GUIRequested: cfg.GUI, LegacyConflict: legacy, ComposeCommand: prefix}
	if candidate != nil {
		s.GUIDeployed, s.ContainerState, s.ContainerID, s.ContainerStarted = deployedGUI(candidate), candidate.State.Status, candidate.ID, candidate.State.Started
	}
	if dockerErr != nil {
		s.DockerError = dockerErr.Error()
	}
	if format == "json" {
		encoder := json.NewEncoder(streams.Out)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(s); err != nil {
			return fail(streams.Err, 1, "%v", err)
		}
		return 0
	}
	fmt.Fprintf(streams.Out, "Harness Corral %s\nWorkspace: %s\nWorkspace ID: %s\nProject: %s\nContainer: %s\nImage: %s\nState: %s (%s)\nGUI deployed: %s\nContainer state: %s\n", Version, workspace.Path, workspace.FullID, workspace.Project, workspace.Project, s.Image, s.StateMode, s.StateVolume, valueOrEmpty(s.GUIDeployed, "none"), valueOrEmpty(s.ContainerState, "absent"))
	if legacy != nil {
		fmt.Fprintf(streams.Out, "myCodex conflict: %s (%s)\n", legacy.Container, legacy.State)
	}
	if dockerErr != nil {
		fmt.Fprintf(streams.Out, "Docker: unavailable (%v)\n", dockerErr)
	}
	return 0
}

func findContainer(containers []containerruntime.Container, name string) *containerruntime.Container {
	for index := range containers {
		if containers[index].CleanName() == name {
			return &containers[index]
		}
	}
	return nil
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
		case workspace.Project:
			cfg.StateMode, cfg.StateVolumeName = config.StatePrivate, ""
		case "hcorral_state":
			cfg.StateMode, cfg.StateVolumeName = config.StateShared, ""
		default:
			cfg.StateMode, cfg.StateVolumeName = config.StateCustom, mount.Name
		}
		return
	}
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
