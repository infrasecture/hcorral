package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/infrasecture/hcorral/internal/command"
	"github.com/infrasecture/hcorral/internal/config"
	"github.com/infrasecture/hcorral/internal/identity"
	"github.com/infrasecture/hcorral/internal/legacyguard"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
	"github.com/infrasecture/hcorral/internal/session"
	"github.com/infrasecture/hcorral/internal/sessionconfig"
	"github.com/infrasecture/hcorral/internal/sessiontransport"
)

type sessionRequest struct {
	operation, id, hostHome, hostSQLite, containerSQLite, format string
	limits                                                       session.Limits
	help                                                         bool
}

func parseSession(args []string) (sessionRequest, error) {
	r := sessionRequest{limits: session.DefaultLimits(), format: "human"}
	if len(args) == 0 {
		return r, errors.New("session requires export or import; see 'hcorral session --help'")
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h" || args[0] == "help") {
		r.help = true
		return r, nil
	}
	r.operation = args[0]
	if r.operation != "export" && r.operation != "import" {
		return r, errors.New("session requires export or import")
	}
	f := flag.NewFlagSet("session "+r.operation, flag.ContinueOnError)
	f.SetOutput(io.Discard)
	f.StringVar(&r.hostSQLite, "host-sqlite-home", "", "effective host SQLite directory")
	f.StringVar(&r.containerSQLite, "container-sqlite-home", "", "effective container SQLite directory")
	f.StringVar(&r.format, "format", "human", "human or json output")
	f.Int64Var(&r.limits.RecordBytes, "record-bytes", r.limits.RecordBytes, "maximum JSONL record size")
	f.Int64Var(&r.limits.FileBytes, "file-bytes", r.limits.FileBytes, "maximum decoded rollout size")
	f.IntVar(&r.limits.Files, "files", r.limits.Files, "maximum lineage files")
	f.Int64Var(&r.limits.ManifestBytes, "manifest-bytes", r.limits.ManifestBytes, "maximum manifest size")
	var positions, options []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positions = append(positions, args[i+1:]...)
			break
		}
		if arg == "--help" || arg == "-h" {
			r.help = true
			return r, nil
		}
		if !strings.HasPrefix(arg, "-") {
			positions = append(positions, arg)
			continue
		}
		name, _, hasValue := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") || f.Lookup(name) == nil {
			return r, fmt.Errorf("unknown session option %q", arg)
		}
		options = append(options, arg)
		if !hasValue {
			if i+1 == len(args) {
				return r, fmt.Errorf("%s requires a value", arg)
			}
			i++
			options = append(options, args[i])
		}
	}
	if err := f.Parse(options); err != nil {
		return r, err
	}
	var emptyOption string
	f.Visit(func(option *flag.Flag) {
		if (option.Name == "host-sqlite-home" || option.Name == "container-sqlite-home") && option.Value.String() == "" {
			emptyOption = option.Name
		}
	})
	if emptyOption != "" {
		return r, fmt.Errorf("--%s must not be empty", emptyOption)
	}
	if len(positions) < 1 || len(positions) > 2 {
		return r, errors.New("expected session UUID and optional host Codex home")
	}
	var err error
	r.id, err = session.ParseID(positions[0])
	if err != nil {
		return r, err
	}
	if len(positions) == 2 {
		r.hostHome = positions[1]
		if r.hostHome == "" {
			return r, errors.New("explicit host Codex home must not be empty")
		}
	}
	if r.format != "human" && r.format != "json" {
		return r, errors.New("session --format must be human or json")
	}
	if r.containerSQLite != "" && !filepath.IsAbs(r.containerSQLite) {
		return r, errors.New("--container-sqlite-home must be absolute in the container")
	}
	if err := r.limits.Validate(); err != nil {
		return r, err
	}
	return r, nil
}

type sessionServices struct {
	home, codexHome, sqliteHome string
	readHost                    sessionconfig.ReadFile
	helper                      func(string) ([]byte, error)
}

func runSessionCommand(cfg config.Config, workspace identity.Workspace, streams Streams, runner command.Runner) int {
	// Capture client-host values before constructing any container environment.
	home, err := os.UserHomeDir()
	if err != nil {
		return fail(streams.Err, 2, "resolve host home: %v", err)
	}
	services := sessionServices{home: home, codexHome: os.Getenv("CODEX_HOME"), sqliteHome: os.Getenv("CODEX_SQLITE_HOME"), readHost: sessionconfig.ReadLocalFile, helper: sessiontransport.ReadHelper}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return runSession(ctx, cfg, workspace, streams, runner, services)
}

func runSession(ctx context.Context, cfg config.Config, workspace identity.Workspace, streams Streams, runner command.Runner, services sessionServices) int {
	request, err := parseSession(cfg.Command[1:])
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if request.help {
		fmt.Fprint(streams.Out, SessionUsage)
		return 0
	}
	if cfg.Harness != "codex" {
		return fail(streams.Err, 2, "session transfer supports Codex corrals; selected harness is %q", cfg.Harness)
	}
	hostHome, err := session.ResolveHostHome(request.hostHome, services.codexHome, services.home, cfg.CallerDir)
	if err != nil {
		return fail(streams.Err, 2, "%v", err)
	}
	if request.hostSQLite != "" && !filepath.IsAbs(request.hostSQLite) {
		request.hostSQLite = filepath.Join(cfg.CallerDir, request.hostSQLite)
	}
	lock, err := identity.AcquireLockContext(ctx, workspace.Project)
	if err != nil {
		return failSession(ctx, streams.Err, err)
	}
	defer lock.Close()
	docker := containerruntime.NewDocker(runner).WithStreams(streams.Out, streams.Err)
	containers, err := docker.ListContainers(ctx)
	if err != nil {
		return failSession(ctx, streams.Err, err)
	}
	if legacy := legacyguard.Find(containers, workspace.Path); legacy != nil {
		return refuseLegacy(streams.Err, legacy)
	}
	candidate := findContainer(containers, workspace.Project)
	if err := identity.VerifyContainer(candidate, workspace); err != nil {
		return failSession(ctx, streams.Err, err)
	}
	if err := verifyProjectContainers(containers, workspace, candidate); err != nil {
		return failSession(ctx, streams.Err, err)
	}
	target, err := sessiontransport.InspectTarget(candidate)
	if err != nil {
		return failSession(ctx, streams.Err, err)
	}
	if cfg.StateSpecified || cfg.Sources["state"] == "environment" {
		want := stateVolumeName(cfg, workspace)
		matched := false
		for _, mount := range candidate.Mounts {
			if mount.Destination == target.Home && mount.Type == "volume" && mount.Name == want {
				matched = true
			}
		}
		if !matched {
			return fail(streams.Err, 2, "requested state volume %q disagrees with the deployed runtime home; select the existing corral's storage without recreating it", want)
		}
	}
	transport := sessiontransport.Docker{Runtime: docker, Helper: services.helper}
	discoveryCtx, stopDiscovery := context.WithTimeout(ctx, 45*time.Second)
	hostState, err := sessionconfig.SQLiteHome(discoveryCtx, sessionconfig.SQLiteOptions{Home: hostHome, CWD: cfg.CallerDir, Environment: services.sqliteHome, Explicit: request.hostSQLite}, services.readHost)
	if err != nil {
		stopDiscovery()
		return failSession(ctx, streams.Err, fmt.Errorf("host SQLite discovery: %w; use --host-sqlite-home for an explicit selection", err))
	}
	containerState, err := transport.ResolveSQLiteHome(discoveryCtx, workspace, target, request.containerSQLite)
	stopDiscovery()
	if err != nil {
		return failSession(ctx, streams.Err, fmt.Errorf("container SQLite discovery: %w; use --container-sqlite-home for an explicit selection", err))
	}
	target, err = sessiontransport.InspectTargetForTransfer(candidate, containerState.Path, request.operation)
	if err != nil {
		return failSession(ctx, streams.Err, err)
	}
	options := sessiontransport.TransferOptions{Operation: request.operation, ThreadID: request.id, HostHome: hostHome, HostSQLiteHome: hostState.Path, ContainerSQLiteHome: containerState.Path, Limits: request.limits}
	result, transferErr := transport.Transfer(ctx, workspace, target, options, streams.Err)
	if result.ThreadID != "" {
		if err := printSessionResult(streams.Out, request, result, hostState, containerState); err != nil {
			transferErr = errors.Join(transferErr, fmt.Errorf("session publication was confirmed but reporting the result failed: %w", err))
		}
	}
	if transferErr != nil {
		return failSession(ctx, streams.Err, transferErr)
	}
	return 0
}

func failSession(ctx context.Context, stderr io.Writer, err error) int {
	code := 1
	// The transport also cancels a peer after a real conflict or I/O failure.
	// Only cancellation of the original invocation means the user interrupted it.
	if errors.Is(ctx.Err(), context.Canceled) {
		code = 130
	}
	return fail(stderr, code, "%v", err)
}

func printSessionResult(out io.Writer, request sessionRequest, result session.Result, hostState, containerState sessionconfig.Resolution) error {
	if request.format == "json" {
		return json.NewEncoder(out).Encode(struct {
			Operation       string                   `json:"operation"`
			HostSQLite      sessionconfig.Resolution `json:"host_sqlite"`
			ContainerSQLite sessionconfig.Resolution `json:"container_sqlite"`
			Result          session.Result           `json:"result"`
			Notes           []string                 `json:"notes"`
		}{request.operation, hostState, containerState, result, []string{
			"Only native conversation history and its required inherited prefixes were copied; credentials, configuration and workspace files were excluded.",
			"Database-only names/metadata and external resources are not transferred. Review the saved workspace and Codex permissions before resuming.",
		}})
	}
	created, reused, extended, prefixes := 0, 0, 0, 0
	for _, file := range result.Files {
		if file.Created {
			created++
		} else if file.Extended {
			extended++
		} else {
			reused++
		}
		if file.Prefix {
			prefixes++
		}
	}
	destination := "host"
	if request.operation == "import" {
		destination = "container"
	}
	var report strings.Builder
	fmt.Fprintf(&report, "Session %s copied to %s Codex home %q.\n", result.ThreadID, destination, result.Destination)
	fmt.Fprintf(&report, "Files: %d created, %d reused, %d compatibly extended; %d inherited-history prefixes.\n", created, reused, extended, prefixes)
	fmt.Fprintf(&report, "SQLite homes: host %q; container %q.\n", hostState.Path, containerState.Path)
	for _, file := range result.Files {
		if file.Promoted {
			fmt.Fprintln(&report, "The previously imported prerequisite is now available as a complete parent conversation; existing children retain their inherited-history boundaries.")
		}
	}
	if result.SelectionRepaired {
		fmt.Fprintln(&report, "The destination Codex index now selects the complete imported conversation instead of its prerequisite.")
	}
	if result.Metadata.CWD != "" {
		fmt.Fprintf(&report, "Saved working directory: %q. Workspace files and local resources were not copied.\n", result.Metadata.CWD)
	}
	if prefixes > 0 {
		fmt.Fprintln(&report, "Inherited prefixes contain only the history needed by this conversation, not complete parent conversations.")
	}
	if result.Archived {
		fmt.Fprintf(&report, "The session remains archived; unarchive %s in destination Codex before resuming.\n", result.ThreadID)
	}
	fmt.Fprintln(&report, "Database-only names/metadata and external resources were not transferred.")
	fmt.Fprintln(&report, "Before resuming, review the destination workspace and Codex permissions. Authentication and configuration were not copied.")
	_, err := io.WriteString(out, report.String())
	return err
}

const SessionUsage = `Usage:
  hcorral [options] session export <session-id> [host-codex-home] [transfer-options]
  hcorral [options] session import <session-id> [host-codex-home] [transfer-options]

Export copies from the selected Codex corral to the host; import copies into it.
The host path defaults to CODEX_HOME, then ~/.codex, on the Docker client machine.
An explicit path names the Codex home itself. Relative host paths use the caller's
directory. The existing corral may be running or stopped. Transfers do not pull,
start or recreate the workstation, attach to tmux, or resume Codex.

Transfer options (before or after the ID/path):
  --host-sqlite-home <path>           Explicit host metadata directory
  --container-sqlite-home <absolute>  Explicit container metadata directory
  --format human|json                Output format (default: human)
  --record-bytes <n>                 Maximum JSONL record bytes (default: 268435456)
  --file-bytes <n>                   Maximum decoded rollout bytes (default: 68719476736)
  --files <n>                        Maximum lineage files (default: 4096)
  --manifest-bytes <n>               Maximum manifest bytes (default: 8388608)
  -h, --help                        Show this help

SQLite discovery uses local base config, local requirements and CODEX_SQLITE_HOME.
Use explicit SQLite paths for database overrides from project config, selected
Codex profiles, runtime flags, cloud policy or macOS managed preferences.
The source conversation can stay open and keep running. Copying captures complete
saved records at a fixed boundary; pending writes and later messages stay in the
original. The session ID is preserved. Existing destination conversations are
never overwritten; a busy destination or divergent history is refused.
VM-shared and network storage with unqualified locks is refused. Keep container
history in daemon-local storage and transfer to/from a native local host home.
Existing divergent history is a conflict; identical content is reused.
Managed inherited prefixes may grow when all prior bytes match; a later
complete-parent import preserves child boundaries.
Supported destination metadata may be repaired to select complete imported history.
Failed publication can retain copied files; retry verifies and reuses them.
Credentials and workspace files are excluded.
`
