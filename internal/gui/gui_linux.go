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

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/compose"
	"github.com/infrasecture/hcorral/internal/identity"
)

var x11DisplayPattern = regexp.MustCompile(`^(?:(?:unix|unix/)?):([0-9]+)(?:\.[0-9]+)?$`)

func (r Resolver) resolvePlatform(ctx context.Context, mode string, workspace identity.Workspace, assets compose.AssetPaths) (Selection, error) {
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
	authority, err := r.copyXAuthority(ctx, workspace, display)
	if err != nil {
		return Selection{}, err
	}
	return Selection{Mode: "x11", File: assets.X11, Env: map[string]string{
		"HCORRAL_GUI_MODE": "x11", "HCORRAL_X11_DISPLAY": display,
		"HCORRAL_X11_SOCKET": socket, "HCORRAL_X11_AUTHORITY": authority,
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

func (r Resolver) copyXAuthority(ctx context.Context, workspace identity.Workspace, display string) (string, error) {
	stateHome := r.Environ("XDG_STATE_HOME")
	if stateHome == "" {
		home := r.Environ("HOME")
		if home == "" {
			return "", errors.New("--gui=x11 requires HOME or XDG_STATE_HOME")
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	if !filepath.IsAbs(stateHome) {
		return "", errors.New("XDG_STATE_HOME must be absolute")
	}
	directory := filepath.Join(stateHome, "hcorral", "gui", workspace.FullID)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create X11 credential directory: %w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("X11 credential directory is not physical: %s", directory)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	result, err := r.Runner.Capture(ctx, []string{"xauth", "nlist", display}, command.EnvironmentWithoutCompose(os.Environ()))
	if err != nil || len(strings.TrimSpace(string(result.Stdout))) == 0 {
		return "", fmt.Errorf("could not read X11 credentials for DISPLAY=%s", display)
	}
	lines := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	for index := range lines {
		if len(lines[index]) < 4 {
			return "", errors.New("xauth returned malformed credential data")
		}
		lines[index] = "ffff" + lines[index][4:]
	}
	temporary, err := os.CreateTemp(directory, "xauthority.*")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		os.Remove(temporaryPath)
		return "", err
	}
	if err := temporary.Close(); err != nil {
		os.Remove(temporaryPath)
		return "", err
	}
	defer os.Remove(temporaryPath)
	input := strings.NewReader(strings.Join(lines, "\n") + "\n")
	if err := r.Runner.Run(ctx, []string{"xauth", "-f", temporaryPath, "nmerge", "-"}, command.EnvironmentWithoutCompose(os.Environ()), input, os.Stderr, os.Stderr); err != nil {
		return "", fmt.Errorf("write copied X11 credentials: %w", err)
	}
	info, err := os.Stat(temporaryPath)
	if err != nil || info.Size() == 0 {
		return "", errors.New("copied X11 credential is empty")
	}
	target := filepath.Join(directory, "xauthority")
	if err := os.Rename(temporaryPath, target); err != nil {
		return "", fmt.Errorf("install copied X11 credential: %w", err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", err
	}
	return target, nil
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
