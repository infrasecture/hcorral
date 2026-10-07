package app

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/update"
)

// The launcher supplies the helper, including when the image predates notices.
// Text travels in separate argv elements and is never evaluated as shell code.
//
//go:embed assets/tmux-notices.sh
var tmuxNotices string

type runtimeSettings struct {
	UID, Home, Session, Workdir string
}

func deployedSettings(cfg config.Config, container *containerruntime.Container) runtimeSettings {
	settings := runtimeSettings{UID: strconv.Itoa(os.Geteuid()), Home: cfg.ContainerHome, Session: cfg.Session, Workdir: cfg.Workdir}
	if container == nil {
		return settings
	}
	for key, target := range map[string]*string{
		"HCORRAL_HOST_UID": &settings.UID, "HCORRAL_CONTAINER_HOME": &settings.Home,
		"HCORRAL_BYOBU_SESSION": &settings.Session, "HCORRAL_WORKDIR": &settings.Workdir,
	} {
		if value := containerEnv(container, key); value != "" {
			*target = value
		}
	}
	return settings
}

type startupReport struct {
	Text      string
	Image     string
	Refreshed bool
}

func attachWithReport(ctx context.Context, cfg config.Config, candidate *containerruntime.Container, startup startupReport, reopen bool, streams Streams, runner command.Runner, docker containerruntime.Docker) int {
	var report strings.Builder
	report.WriteString(startup.Text)
	// An unavailable render is an unknown selected image, not permission to
	// describe a different configured reference as the effective overlay image.
	cfg.Image = startup.Image
	if !reopen {
		update.Checker{Docker: docker, Out: io.MultiWriter(streams.Err, &report), LauncherVersion: Version, ImageRefreshed: startup.Refreshed}.Notify(ctx, cfg, candidate)
	}
	return replaceAttach(cfg, candidate, report.String(), reopen, streams, runner)
}

func replaceAttach(cfg config.Config, container *containerruntime.Container, details string, reopen bool, streams Streams, runner command.Runner) int {
	settings := deployedSettings(cfg, container)
	mode := deployedGUI(container)
	notice := "hcorral: GUI access disabled (headless)"
	switch mode {
	case "x11":
		notice = "hcorral: GUI access enabled (X11); the container can access the selected desktop display."
	case "wayland":
		notice = "hcorral: GUI access enabled (Wayland); the container can access the selected desktop socket."
	case "none":
	default:
		notice = fmt.Sprintf("hcorral: deployed GUI mode is %q (unknown)", mode)
	}
	if !reopen {
		fmt.Fprintln(streams.Err, notice)
	}
	reopenArg := "0"
	if reopen {
		reopenArg = "1"
	}
	argv := []string{"docker", "exec", "-it", container.ID, "gosu", settings.UID, "env", "HOME=" + settings.Home,
		"bash", "--noprofile", "--norc", "-c", tmuxNotices, "hcorral-tmux", settings.Session, mode, notice, details, reopenArg}
	if err := runner.Replace(argv, command.EnvironmentWithoutCompose(os.Environ())); err != nil {
		return fail(streams.Err, 1, "attach: %v", err)
	}
	return 0
}
