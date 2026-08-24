package app

import (
	"fmt"
	"io"
	"os"
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

	cfg, err := config.Parse(args, config.ParseOptions{CallerDir: caller, HomeDir: home, Platform: runtime.GOOS, Getenv: os.Getenv})
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if len(cfg.Command) == 1 && cfg.Command[0] == "help" {
		fmt.Fprint(streams.Out, Usage)
		return 0
	}
	if len(cfg.Command) > 0 && cfg.Command[0] == "version" {
		fmt.Fprintf(streams.Out, "hcorral %s (%s)\n", Version, Commit)
		return 0
	}

	workspace, err := identity.Resolve(cfg.CallerDir, cfg.Workspace, cfg.ProjectName)
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if cfg.Workdir == cfg.Workspace {
		cfg.Workdir = workspace.Path
	}
	cfg.Workspace = workspace.Path
	return runOperational(cfg, workspace, streams, command.ExecRunner{})
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

const Usage = `Usage:
  hcorral [options]                  Start if needed, then attach
  hcorral [options] attach           Attach to the workstation session
  hcorral [options] info [--format=human|json]
  hcorral [options] ps|start|stop|restart|pull|up|create|down [args...]
  hcorral [options] exec <cmd...>
  hcorral [options] <compose-command> [args...]
  hcorral version

Options:
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
