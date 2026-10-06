// Package sessionhelper is the narrow stdio interface shared by the launcher
// and its Linux helper. It does not discover Docker resources, initialize a
// workstation, read credentials, or start Codex.
package sessionhelper

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/infrasecture/hcorral/internal/session"
)

type Capabilities struct {
	Protocol int    `json:"protocol"`
	OS       string `json:"os"`
	Arch     string `json:"arch"`
}

// Run speaks only the explicitly versioned internal protocol. Export stdout is
// the transfer stream; import stdout is one result object. The caller supplies
// already resolved endpoint paths, including the effective SQLite directory.
// The caller also owns closing blocking pipes when ctx is cancelled.
func Run(ctx context.Context, args []string, input io.Reader, output io.Writer) (resultErr error) {
	if len(args) == 1 && args[0] == "protocol" {
		return json.NewEncoder(output).Encode(Capabilities{session.ProtocolVersion, runtime.GOOS, runtime.GOARCH})
	}
	if len(args) == 0 || (args[0] != "export" && args[0] != "import") {
		return errors.New("expected protocol, export or import operation")
	}
	flags := flag.NewFlagSet("hcorral-session", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	protocol := flags.Int("protocol", 0, "required transfer protocol version")
	homePath := flags.String("home", "", "absolute Codex home")
	sqlitePath := flags.String("sqlite-home", "", "absolute effective Codex SQLite home")
	idArg := flags.String("id", "", "requested thread UUID")
	limits := session.DefaultLimits()
	flags.Int64Var(&limits.RecordBytes, "record-bytes", limits.RecordBytes, "maximum JSONL record bytes")
	flags.Int64Var(&limits.FileBytes, "file-bytes", limits.FileBytes, "maximum decoded rollout bytes")
	flags.IntVar(&limits.Files, "files", limits.Files, "maximum lineage files")
	flags.Int64Var(&limits.ManifestBytes, "manifest-bytes", limits.ManifestBytes, "maximum manifest bytes")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *protocol != session.ProtocolVersion {
		return errors.New("unexpected arguments or incompatible transfer protocol")
	}
	if !filepath.IsAbs(*homePath) || !filepath.IsAbs(*sqlitePath) {
		return errors.New("absolute --home and --sqlite-home paths are required")
	}
	id, err := session.ParseID(*idArg)
	if err != nil {
		return err
	}
	if err := limits.Validate(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if args[0] == "import" {
		// The explicitly selected destination may be new. Existing roots,
		// including intentional root symlinks, retain their permissions.
		// No source root or relocated database directory is initialized.
		if err := os.MkdirAll(*homePath, 0o700); err != nil {
			return fmt.Errorf("create destination Codex home %s as UID %d GID %d: %w", *homePath, os.Geteuid(), os.Getegid(), err)
		}
	}
	home, err := session.OpenHome(*homePath)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, home.Close()) }()
	database, err := session.OpenHome(*sqlitePath)
	if err != nil {
		return fmt.Errorf("open effective SQLite home: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	if args[0] == "export" {
		snapshot, err := home.Snapshot(ctx, id, database, limits)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
		return snapshot.Export(ctx, output)
	}
	incoming, err := home.Receive(ctx, input, limits)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, incoming.Close()) }()
	if incoming.Plan.ThreadID != id {
		return errors.New("received a different thread than the requested UUID")
	}
	result, err := incoming.Publish(ctx, database)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(result)
}
