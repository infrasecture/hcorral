package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
)

var (
	Version = "devel"
	Commit  = "unknown"
)

type Streams struct {
	In  io.Reader
	Out io.Writer
	Err io.Writer
}

func Run(args []string, streams Streams) int {
	caller, err := os.Getwd()
	if err != nil {
		return fail(streams.Err, 1, "resolve caller directory: %v", err)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(streams.Err, 2, "resolve user home: %v", err)
	}

	cfg, err := config.Parse(args, config.ParseOptions{CallerDir: caller, HomeDir: home, Platform: runtime.GOOS, Getenv: os.Getenv, Environ: os.Environ})
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	for _, warning := range cfg.Warnings {
		fmt.Fprintf(streams.Err, "hcorral: warning: %s\n", warning)
	}
	if len(cfg.Command) == 1 && cfg.Command[0] == "help" {
		fmt.Fprint(streams.Out, Usage)
		return 0
	}
	if len(cfg.Command) > 0 && cfg.Command[0] == "version" {
		fmt.Fprintf(streams.Out, "hcorral %s (%s)\n", Version, Commit)
		return 0
	}

	workspace, err := identity.Resolve(cfg.CallerDir, cfg.Workspace, cfg.Harness, cfg.ProjectName)
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	cfg.Workdir, err = normalizeWorkdir(cfg.Workdir, cfg.Workspace, cfg.ContainerHome, workspace.Path)
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	cfg.Workspace = workspace.Path
	return runOperational(cfg, workspace, streams, command.ExecRunner{})
}

func normalizeWorkdir(workdir, logicalWorkspace, containerHome, physicalWorkspace string) (string, error) {
	workdir = filepath.Clean(workdir)
	logicalWorkspace = filepath.Clean(logicalWorkspace)
	containerHome = filepath.Clean(containerHome)
	physicalWorkspace = filepath.Clean(physicalWorkspace)

	if relative, inside := relativeWithin(logicalWorkspace, workdir); inside {
		candidate := filepath.Join(physicalWorkspace, relative)
		physical, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			return "", fmt.Errorf("resolve workdir %q: %w", workdir, err)
		}
		if _, inside := relativeWithin(physicalWorkspace, physical); !inside {
			return "", fmt.Errorf("workdir %q escapes the physical workspace through a symlink", workdir)
		}
		info, err := os.Stat(physical)
		if err != nil || !info.IsDir() {
			return "", fmt.Errorf("workspace workdir %q is not an existing directory", workdir)
		}
		return physical, nil
	}
	if _, inside := relativeWithin(containerHome, workdir); inside {
		return workdir, nil
	}
	return "", fmt.Errorf("workdir %q must be within the mounted workspace %q or container home %q", workdir, physicalWorkspace, containerHome)
}

func relativeWithin(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || filepath.IsAbs(relative) || len(relative) > 3 && relative[:3] == ".."+string(filepath.Separator) {
		return "", false
	}
	return relative, true
}

func commandName(command []string) string {
	if len(command) == 0 {
		return "attach"
	}
	return command[0]
}

func fail(writer io.Writer, code int, format string, args ...any) int {
	fmt.Fprintf(writer, "hcorral: "+format+"\n", args...)
	return code
}

func childExitCode(err error) int {
	var exitError *exec.ExitError
	if errors.As(err, &exitError) && exitError.ExitCode() >= 0 {
		return exitError.ExitCode()
	}
	return 1
}

const Usage = `Usage:
  hcorral [options]                  Start if needed, then attach
  hcorral [options] attach           Attach to the workstation session
  hcorral [options] info [--format=human|json]
  hcorral [options] state rm --scope global|workspace
  hcorral [options] ps|start|stop|restart|pull|up|create|down [args...]
  hcorral [options] exec <cmd...>
  hcorral [options] <compose-command> [args...]
  hcorral version

Options:
  --harness <type>                   Select codex, claude, pi, or a custom type
  --image <full-oci-reference>       Select the full image reference
  --workspace <path>                 Select a workspace without changing directory
  --project-name <name>              Override the generated Compose project name
  --state-volume <name>              Use an explicitly managed state volume
  --private-env                      Use per-workspace private state
  --gui[=auto|x11|wayland]           Enable Linux GUI forwarding
  --no-gui                           Explicitly select headless mode
  -v, --volume <source:target[:mode]> Add a bind or volume mount
  -f, --compose-file <path>          Add an ordered Compose overlay
  -h, --help                         Show this help
`
