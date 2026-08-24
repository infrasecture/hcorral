package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

type Project struct {
	Runner     command.Runner
	Invocation Invocation
	Out        io.Writer
	Err        io.Writer
}

type Rendered struct {
	Services map[string]RenderedService
	Volumes  map[string]RenderedTopLevelVolume
	Networks map[string]RenderedTopLevelNetwork
	Hashes   map[string]string
}

type RenderedTopLevelVolume struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type RenderedTopLevelNetwork struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type RenderedService struct {
	Image         string            `json:"image"`
	ContainerName string            `json:"container_name"`
	WorkingDir    string            `json:"working_dir"`
	Restart       string            `json:"restart"`
	Init          bool              `json:"init"`
	Tty           bool              `json:"tty"`
	StdinOpen     bool              `json:"stdin_open"`
	User          string            `json:"user"`
	ReadOnly      bool              `json:"read_only"`
	Privileged    bool              `json:"privileged"`
	NetworkMode   string            `json:"network_mode"`
	PIDMode       string            `json:"pid"`
	IPCMode       string            `json:"ipc"`
	UserNSMode    string            `json:"userns_mode"`
	UTSMode       string            `json:"uts"`
	CgroupMode    string            `json:"cgroup"`
	Runtime       string            `json:"runtime"`
	CapAdd        []string          `json:"cap_add"`
	GroupAdd      []string          `json:"group_add"`
	Devices       []RenderedDevice  `json:"devices"`
	SecurityOpt   []string          `json:"security_opt"`
	VolumesFrom   []string          `json:"volumes_from"`
	Tmpfs         []string          `json:"tmpfs"`
	Configs       []RenderedFile    `json:"configs"`
	Secrets       []RenderedFile    `json:"secrets"`
	GPUs          json.RawMessage   `json:"gpus"`
	UseAPISocket  bool              `json:"use_api_socket"`
	Entrypoint    json.RawMessage   `json:"entrypoint"`
	Command       json.RawMessage   `json:"command"`
	Labels        map[string]string `json:"labels"`
	Environment   map[string]string `json:"environment"`
	Volumes       []RenderedVolume  `json:"volumes"`
}

type RenderedDevice struct {
	Source      string `json:"source"`
	Target      string `json:"target"`
	Permissions string `json:"permissions"`
}

type RenderedFile struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type RenderedVolume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only"`
}

func (p Project) Capture(ctx context.Context, args ...string) (command.Result, error) {
	argv := p.Invocation.Args(args...)
	result, err := p.Runner.Capture(ctx, argv, p.Invocation.Env)
	if err != nil {
		// Compose diagnostics may contain interpolated secrets from user overlays.
		// Keep captured output available to the caller for deliberate handling, but
		// never promote it into a launcher error or structured information output.
		return result, fmt.Errorf("Compose %s failed: %w", first(args), err)
	}
	return result, nil
}

func (p Project) Run(ctx context.Context, args ...string) error {
	argv := p.Invocation.Args(args...)
	if err := p.Runner.Run(ctx, argv, p.Invocation.Env, nil, p.Out, p.Err); err != nil {
		return fmt.Errorf("Compose %s failed: %w", first(args), err)
	}
	return nil
}

func (p Project) Replace(args ...string) error {
	return p.Runner.Replace(p.Invocation.Args(args...), p.Invocation.Env)
}

func (p Project) RenderAndValidate(ctx context.Context, cfg config.Config, workspace identity.Workspace, guiMode, stateVolume string) (Rendered, error) {
	result, err := p.Capture(ctx, "config", "--format", "json")
	if err != nil {
		return Rendered{}, err
	}
	var document struct {
		Services map[string]RenderedService         `json:"services"`
		Volumes  map[string]RenderedTopLevelVolume  `json:"volumes"`
		Networks map[string]RenderedTopLevelNetwork `json:"networks"`
	}
	if err := json.Unmarshal(result.Stdout, &document); err != nil {
		return Rendered{}, fmt.Errorf("decode rendered Compose project: %w", err)
	}
	service, ok := document.Services["hcorral"]
	if !ok {
		return Rendered{}, fmt.Errorf("rendered Compose project removed required hcorral service")
	}
	wants := map[string]string{
		"image":          cfg.ImageName + ":" + cfg.ImageTag,
		"container_name": workspace.Project,
		"working_dir":    cfg.Workdir,
	}
	if service.Image != wants["image"] {
		return Rendered{}, fmt.Errorf("overlay changed managed image: got %q want %q", service.Image, wants["image"])
	}
	if service.ContainerName != wants["container_name"] {
		return Rendered{}, fmt.Errorf("overlay changed managed container name: got %q want %q", service.ContainerName, wants["container_name"])
	}
	if service.WorkingDir != wants["working_dir"] {
		return Rendered{}, fmt.Errorf("overlay changed managed working directory: got %q want %q", service.WorkingDir, wants["working_dir"])
	}
	if service.Restart != "unless-stopped" || !service.Init || !service.Tty || !service.StdinOpen {
		return Rendered{}, fmt.Errorf("overlay changed managed service lifecycle settings")
	}
	if service.User != "" || service.ReadOnly || service.Privileged || service.NetworkMode != "" || service.PIDMode != "" || service.IPCMode != "" || service.UserNSMode != "" || service.UTSMode != "" || service.CgroupMode != "" || service.Runtime != "" || len(service.CapAdd) != 0 || len(service.GroupAdd) != 0 || len(service.Devices) != 0 || len(service.SecurityOpt) != 0 || len(service.VolumesFrom) != 0 || !nullJSON(service.GPUs) || service.UseAPISocket {
		return Rendered{}, fmt.Errorf("overlay changed managed service privilege, namespace, user, or filesystem settings")
	}
	if !nullJSON(service.Entrypoint) || !nullJSON(service.Command) {
		return Rendered{}, fmt.Errorf("overlay changed managed image entrypoint or command")
	}
	for label, want := range map[string]string{
		identity.LabelWorkspaceID:     workspace.FullID,
		identity.LabelWorkspaceScheme: identity.WorkspaceSchemeVersion,
		identity.LabelRuntimeSchema:   identity.RuntimeSchemaVersion,
		identity.LabelGUI:             guiMode,
	} {
		if service.Labels[label] != want {
			return Rendered{}, fmt.Errorf("overlay changed managed label %s: got %q want %q", label, service.Labels[label], want)
		}
	}
	for label := range service.Labels {
		if strings.HasPrefix(label, "ai.infrasecture.hcorral.") {
			switch label {
			case identity.LabelWorkspaceID, identity.LabelWorkspaceScheme, identity.LabelRuntimeSchema, identity.LabelGUI:
			default:
				return Rendered{}, fmt.Errorf("overlay added reserved ownership label %s", label)
			}
		}
	}
	expectedEnvironment := map[string]string{
		"HCORRAL_LAUNCHED_BY_WRAPPER": "1",
		"HCORRAL_HOST_UID":            p.invocationEnv("HCORRAL_HOST_UID"),
		"HCORRAL_HOST_GID":            p.invocationEnv("HCORRAL_HOST_GID"),
		"HCORRAL_HOST_USER":           p.invocationEnv("HCORRAL_HOST_USER"),
		"HCORRAL_HOST_GROUP":          p.invocationEnv("HCORRAL_HOST_GROUP"),
		"HCORRAL_HOST_GROUPS":         p.invocationEnv("HCORRAL_HOST_GROUPS"),
		"HCORRAL_CONTAINER_HOME":      cfg.ContainerHome,
		"HCORRAL_WORKDIR":             cfg.Workdir,
		"CODEX_HOME":                  cfg.ContainerHome + "/.codex",
		"HCORRAL_BYOBU_SESSION":       cfg.Session,
		"HCORRAL_AUTO_ATTACH":         p.invocationEnv("HCORRAL_AUTO_ATTACH"),
		"HCORRAL_ATTACH_HINT":         "hcorral",
	}
	for key, want := range expectedEnvironment {
		if service.Environment[key] != want {
			return Rendered{}, fmt.Errorf("overlay changed managed environment %s", key)
		}
	}
	for key := range service.Environment {
		if strings.HasPrefix(key, "HCORRAL_") {
			if _, expected := expectedEnvironment[key]; !expected {
				return Rendered{}, fmt.Errorf("overlay added reserved environment %s", key)
			}
		}
	}
	if !hasVolume(service.Volumes, "bind", cfg.Workspace, cfg.Workspace, false) {
		return Rendered{}, fmt.Errorf("overlay changed managed workspace mount; rendered volumes: %#v", service.Volumes)
	}
	if !hasVolume(service.Volumes, "volume", "hcorral_state", cfg.ContainerHome, false) {
		return Rendered{}, fmt.Errorf("overlay changed managed state mount; rendered volumes: %#v", service.Volumes)
	}
	for _, specification := range cfg.ExtraVolumes {
		normalized, _, err := normalizeMount(cfg.CallerDir, specification)
		if err != nil {
			return Rendered{}, err
		}
		parts := strings.Split(normalized, ":")
		kind := "volume"
		source := parts[0]
		if filepath.IsAbs(parts[0]) {
			kind = "bind"
		} else {
			source = extraVolumeLogicalName(parts[0])
			definition, ok := document.Volumes[source]
			if !ok || !definition.External || definition.Name != parts[0] {
				return Rendered{}, fmt.Errorf("overlay changed managed external extra volume %s", parts[0])
			}
		}
		readOnly := len(parts) == 3 && containsMode(parts[2], "ro")
		if !hasVolume(service.Volumes, kind, source, parts[1], readOnly) {
			return Rendered{}, fmt.Errorf("overlay changed managed extra mount target %s", parts[1])
		}
	}
	managedPaths := []string{cfg.Workspace, cfg.ContainerHome, cfg.Workdir, "/workspace", "/tmp/.hcorral-xauthority", "/tmp/.hcorral-wayland", "/tmp/.X11-unix", "/etc/hcorral", "/usr/local/bin/entrypoint.sh", "/usr/local/bin/hcorral-session-init", "/run/hcorral-startup-status"}
	for _, volume := range service.Volumes {
		if (volume.Type == "bind" && volume.Source == cfg.Workspace && volume.Target == cfg.Workspace) || (volume.Type == "volume" && volume.Source == "hcorral_state" && volume.Target == cfg.ContainerHome) {
			continue
		}
		for _, reserved := range managedPaths {
			if pathsOverlap(volume.Target, reserved) && !expectedGUIMount(p, volume, guiMode) {
				return Rendered{}, fmt.Errorf("overlay added a mount overlapping managed path %s", reserved)
			}
		}
		if prohibitedHostBridge(p, volume, guiMode) {
			return Rendered{}, fmt.Errorf("overlay added a forbidden host GUI, runtime, audio, D-Bus, or GPU mount: %s", volume.Source)
		}
	}
	for _, file := range service.Configs {
		target := file.Target
		if target == "" {
			target = "/" + file.Source
		}
		for _, reserved := range managedPaths {
			if pathsOverlap(target, reserved) {
				return Rendered{}, fmt.Errorf("overlay added a config overlapping managed path %s", reserved)
			}
		}
	}
	for _, file := range service.Secrets {
		target := file.Target
		if target == "" {
			target = "/run/secrets/" + file.Source
		}
		for _, reserved := range managedPaths {
			if pathsOverlap(target, reserved) {
				return Rendered{}, fmt.Errorf("overlay added a secret overlapping managed path %s", reserved)
			}
		}
	}
	for _, specification := range service.Tmpfs {
		target := strings.SplitN(specification, ":", 2)[0]
		for _, reserved := range managedPaths {
			if pathsOverlap(target, reserved) {
				return Rendered{}, fmt.Errorf("overlay added tmpfs overlapping managed path %s", reserved)
			}
		}
	}
	if err := p.validateGUI(service, guiMode); err != nil {
		return Rendered{}, err
	}
	stateDefinition, ok := document.Volumes["hcorral_state"]
	if !ok || !stateDefinition.External || stateDefinition.Name != stateVolume {
		return Rendered{}, fmt.Errorf("overlay changed managed state volume: got %#v, want external name %q", stateDefinition, stateVolume)
	}
	return Rendered{Services: document.Services, Volumes: document.Volumes, Networks: document.Networks, Hashes: p.hashes(ctx)}, nil
}

func prohibitedHostBridge(p Project, volume RenderedVolume, guiMode string) bool {
	if expectedGUIMount(p, volume, guiMode) || volume.Type != "bind" || !filepath.IsAbs(volume.Source) {
		return false
	}
	forbidden := []string{"/tmp/.X11-unix", "/run/dbus", "/var/run/dbus", "/dev/dri", "/dev/snd", "/run/docker.sock", "/var/run/docker.sock", "/run/podman/podman.sock"}
	if runtimeDir := p.invocationEnv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtimeDir) {
		forbidden = append(forbidden, runtimeDir)
	}
	for _, path := range forbidden {
		if pathsOverlap(volume.Source, path) {
			return true
		}
	}
	return false
}

func expectedGUIMount(p Project, volume RenderedVolume, mode string) bool {
	switch mode {
	case "x11":
		return (volume.Source == p.invocationEnv("HCORRAL_X11_SOCKET") && volume.Target == p.invocationEnv("HCORRAL_X11_SOCKET") && volume.ReadOnly) ||
			(volume.Source == p.invocationEnv("HCORRAL_X11_AUTHORITY") && volume.Target == "/tmp/.hcorral-xauthority" && volume.ReadOnly)
	case "wayland":
		return volume.Source == p.invocationEnv("HCORRAL_WAYLAND_SOCKET") && volume.Target == "/tmp/.hcorral-wayland" && volume.ReadOnly
	}
	return false
}

func containsMode(modes, want string) bool {
	for _, mode := range strings.Split(modes, ",") {
		if mode == want {
			return true
		}
	}
	return false
}

func (p Project) validateGUI(service RenderedService, mode string) error {
	switch mode {
	case "none":
		for _, key := range []string{"DISPLAY", "XAUTHORITY", "WAYLAND_DISPLAY", "XDG_SESSION_TYPE"} {
			if _, present := service.Environment[key]; present {
				return fmt.Errorf("overlay crossed headless GUI boundary with environment %s", key)
			}
		}
		for _, volume := range service.Volumes {
			if volume.Target == "/tmp/.hcorral-xauthority" || volume.Target == "/tmp/.hcorral-wayland" || volume.Target == "/tmp/.X11-unix" {
				return fmt.Errorf("overlay crossed headless GUI boundary with mount target %s", volume.Target)
			}
		}
	case "x11":
		if service.Environment["DISPLAY"] != p.invocationEnv("HCORRAL_X11_DISPLAY") || service.Environment["XAUTHORITY"] != "/tmp/.hcorral-xauthority" {
			return fmt.Errorf("overlay changed managed X11 environment")
		}
		if !hasVolume(service.Volumes, "bind", p.invocationEnv("HCORRAL_X11_SOCKET"), p.invocationEnv("HCORRAL_X11_SOCKET"), true) ||
			!hasVolume(service.Volumes, "bind", p.invocationEnv("HCORRAL_X11_AUTHORITY"), "/tmp/.hcorral-xauthority", true) {
			return fmt.Errorf("overlay changed managed X11 mounts")
		}
	case "wayland":
		if service.Environment["WAYLAND_DISPLAY"] != "/tmp/.hcorral-wayland" || service.Environment["XDG_SESSION_TYPE"] != "wayland" {
			return fmt.Errorf("overlay changed managed Wayland environment")
		}
		if !hasVolume(service.Volumes, "bind", p.invocationEnv("HCORRAL_WAYLAND_SOCKET"), "/tmp/.hcorral-wayland", true) {
			return fmt.Errorf("overlay changed managed Wayland mount")
		}
	default:
		return fmt.Errorf("unsupported rendered GUI mode %q", mode)
	}
	return nil
}

func (p Project) invocationEnv(key string) string {
	prefix := key + "="
	for index := len(p.Invocation.Env) - 1; index >= 0; index-- {
		if strings.HasPrefix(p.Invocation.Env[index], prefix) {
			return strings.TrimPrefix(p.Invocation.Env[index], prefix)
		}
	}
	return ""
}

func (p Project) hashes(ctx context.Context) map[string]string {
	result, err := p.Capture(ctx, "config", "--hash", "*")
	if err != nil {
		return nil
	}
	hashes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 {
			hashes[fields[0]] = fields[1]
		}
	}
	return hashes
}

func hasVolume(volumes []RenderedVolume, kind, source, target string, readOnly bool) bool {
	for _, volume := range volumes {
		if volume.Type == kind && volume.Source == source && volume.Target == target && volume.ReadOnly == readOnly {
			return true
		}
	}
	return false
}

func nullJSON(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed == "" || trimmed == "null"
}

func first(values []string) string {
	if len(values) == 0 {
		return "command"
	}
	return values[0]
}
