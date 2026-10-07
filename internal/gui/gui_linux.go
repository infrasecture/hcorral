//go:build linux

package gui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/identity"
)

var x11DisplayPattern = regexp.MustCompile(`^(?:(?:unix|unix/)?):([0-9]+)(?:\.[0-9]+)?$`)

func (r Resolver) resolvePlatform(ctx context.Context, mode string, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
	if r.Runner != nil {
		if err := r.requireLocalEngine(ctx); err != nil {
			return Selection{}, err
		}
	}
	switch mode {
	case "auto":
		if selection, err := r.wayland(assets); err == nil {
			return selection, nil
		}
		return r.x11(ctx, workspace, assets)
	case "x11":
		return r.x11(ctx, workspace, assets)
	case "wayland":
		return r.wayland(assets)
	default:
		return unsupported(mode)
	}
}

func (r Resolver) requireLocalEngine(ctx context.Context) error {
	// Docker's explicit context overrides DOCKER_HOST. Otherwise DOCKER_HOST
	// overrides the stored current context, including when that context is local.
	host := r.Environ("DOCKER_HOST")
	selectedContext := r.Environ("DOCKER_CONTEXT")
	if selectedContext != "" || host == "" {
		argv := []string{"docker", "context", "inspect"}
		if selectedContext != "" {
			argv = append(argv, selectedContext)
		}
		argv = append(argv, "--format", "{{.Endpoints.docker.Host}}")
		result, err := r.Runner.Capture(ctx, argv, command.EnvironmentWithoutCompose(os.Environ()))
		if err != nil {
			return fmt.Errorf("inspect Docker context for GUI forwarding: %w", err)
		}
		host = strings.TrimSpace(string(result.Stdout))
	}
	if !strings.HasPrefix(host, "unix:///") {
		return fmt.Errorf("GUI forwarding requires a local Unix-socket Docker daemon, selected endpoint is %q", host)
	}
	result, err := r.Runner.Capture(ctx, []string{"docker", "info", "--format", "{{.OperatingSystem}}"}, command.EnvironmentWithoutCompose(os.Environ()))
	if err != nil {
		return fmt.Errorf("verify native Docker Engine for GUI forwarding: %w", err)
	}
	daemonOS := strings.TrimSpace(string(result.Stdout))
	if daemonOS == "" || strings.Contains(strings.ToLower(daemonOS), "docker desktop") {
		return errors.New("GUI forwarding requires a verified native local Docker Engine; Docker Desktop is unsupported")
	}
	return nil
}

func (r Resolver) x11(ctx context.Context, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
	display := r.Environ("DISPLAY")
	match := x11DisplayPattern.FindStringSubmatch(display)
	if match == nil {
		return Selection{}, fmt.Errorf("--gui=x11 requires a local Unix DISPLAY, got %q", display)
	}
	socket := "/tmp/.X11-unix/X" + match[1]
	if err := requireSocket(socket); err != nil {
		return Selection{}, fmt.Errorf("selected X11 socket: %w", err)
	}
	stateHome, authority, err := r.xAuthority(ctx, display)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Mode: "x11", File: assets.X11, authority: authority, stateHome: stateHome, project: workspace.Project, Env: map[string]string{
		"HCORRAL_GUI_MODE": "x11", "HCORRAL_X11_DISPLAY": display,
		"HCORRAL_X11_SOCKET": socket, "HCORRAL_X11_AUTHORITY": filepath.Join(stateHome, "hcorral", "gui", workspace.Project, "xauthority"),
	}}, nil
}

func (r Resolver) wayland(assets compose.AssetPaths) (Selection, error) {
	display := r.Environ("WAYLAND_DISPLAY")
	if display == "" || strings.ContainsAny(display, "\x00\r\n") {
		return Selection{}, errors.New("--gui=wayland requires WAYLAND_DISPLAY")
	}
	var socket string
	if filepath.IsAbs(display) {
		socket = filepath.Clean(display)
	} else {
		if strings.ContainsRune(display, '/') {
			return Selection{}, errors.New("relative WAYLAND_DISPLAY must be one socket basename")
		}
		runtimeDir := r.Environ("XDG_RUNTIME_DIR")
		if runtimeDir == "" || !filepath.IsAbs(runtimeDir) {
			return Selection{}, errors.New("--gui=wayland requires an absolute XDG_RUNTIME_DIR")
		}
		socket = filepath.Join(runtimeDir, display)
	}
	if err := requireSocketOwnedBy(socket, r.UID); err != nil {
		return Selection{}, fmt.Errorf("selected Wayland socket: %w", err)
	}
	return Selection{Mode: "wayland", File: assets.Wayland, Env: map[string]string{
		"HCORRAL_GUI_MODE": "wayland", "HCORRAL_WAYLAND_SOCKET": socket,
	}}, nil
}

func (r Resolver) xAuthority(ctx context.Context, display string) (string, []byte, error) {
	stateHome := r.Environ("XDG_STATE_HOME")
	if stateHome == "" {
		home := r.Environ("HOME")
		if home == "" {
			return "", nil, errors.New("--gui=x11 requires HOME or XDG_STATE_HOME")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateHome) {
		return "", nil, errors.New("XDG_STATE_HOME must be absolute")
	}
	result, err := r.Runner.Capture(ctx, []string{"xauth", "nlist", display}, command.EnvironmentWithoutCompose(os.Environ()))
	if err != nil || len(strings.TrimSpace(string(result.Stdout))) == 0 {
		return "", nil, fmt.Errorf("could not read X11 credentials for DISPLAY=%s", display)
	}
	lines := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	for index := range lines {
		if len(lines[index]) < 4 {
			return "", nil, errors.New("xauth returned malformed credential data")
		}
		lines[index] = "ffff" + lines[index][4:]
	}
	return stateHome, []byte(strings.Join(lines, "\n") + "\n"), nil
}

// Prepare is used only when applying a GUI configuration. Discovery and attach
// never replace the credential file mounted by an existing container.
func (r Resolver) Prepare(ctx context.Context, selection Selection) error {
	if selection.Mode != "x11" || len(selection.authority) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	directory := filepath.Join(selection.stateHome, "hcorral", "gui", selection.project)
	if err := secureStateDirectory(selection.stateHome, "hcorral", "gui", selection.project); err != nil {
		return fmt.Errorf("create X11 credential directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, "xauthority.*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return err
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return err
	}
	defer os.Remove(temporaryPath)
	input := strings.NewReader(string(selection.authority))
	if err := r.Runner.Run(ctx, []string{"xauth", "-f", temporaryPath, "nmerge", "-"}, command.EnvironmentWithoutCompose(os.Environ()), input, os.Stderr, os.Stderr); err != nil {
		return fmt.Errorf("write copied X11 credentials: %w", err)
	}
	info, err := os.Stat(temporaryPath)
	if err != nil || info.Size() == 0 {
		return errors.New("copied X11 credential is empty")
	}
	target := filepath.Join(directory, "xauthority")
	if err := os.Rename(temporaryPath, target); err != nil {
		return fmt.Errorf("install copied X11 credential: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return err
	}
	return nil
}

func secureStateDirectory(root string, components ...string) error {
	info, err := os.Lstat(root)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(root, 0o700); err != nil {
			return err
		}
		info, err = os.Lstat(root)
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("state root is not a physical directory: %s", root)
	}
	current := root
	for _, component := range components {
		current = filepath.Join(current, component)
		if err := os.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := os.Lstat(current)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("state path is not a physical directory: %s", current)
		}
	}
	return nil
}

func requireSocket(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("socket does not exist: %s", path)
		}
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("not a Unix socket: %s", path)
	}
	if err := syscall.Access(path, 2); err != nil { // W_OK: connecting requires socket write access.
		return fmt.Errorf("Unix socket is not writable: %s: %w", path, err)
	}
	return nil
}

func requireSocketOwnedBy(path string, uid int) error {
	if err := requireSocket(path); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return fmt.Errorf("socket is not owned by invoking UID %d", uid)
	}
	return nil
}
