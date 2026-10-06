package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var ErrConflict = errors.New("destination session conflicts with the incoming conversation")

type InstalledFile struct {
	Path      string `json:"path"`
	RolloutID string `json:"rollout_id"`
	Prefix    bool   `json:"prefix"`
	Created   bool   `json:"created"`
	Extended  bool   `json:"extended,omitempty"`
}

type Result struct {
	ThreadID    string          `json:"thread_id"`
	Destination string          `json:"destination"`
	MainPath    string          `json:"main_path"`
	Archived    bool            `json:"archived"`
	Files       []InstalledFile `json:"files"`
	Metadata    Metadata        `json:"metadata"`
}

type choice struct {
	path       string
	exists     bool
	extensions []prefixExtension
}

// Publish validates every existing identity before installing any live file.
// New rollouts use exclusive hard links from same-filesystem staging.
// A managed prerequisite may grow atomically after its entire
// existing prefix is verified against the incoming bytes. Writer guards remain
// held through validation and publication.
func (in *Incoming) Publish(ctx context.Context, sqliteHome *Home) (_ Result, resultErr error) {
	if in.stage == nil || len(in.Plan.Files) == 0 {
		return Result{}, errors.New("incoming session is empty or closed")
	}
	if sqliteHome == nil {
		return Result{}, errors.New("effective destination SQLite home is required")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	guard := &writerGuards{home: in.home, files: make(map[string]*os.File)}
	defer guard.Close()
	var ids []string
	for _, file := range in.Plan.Files {
		ids = append(ids, file.ThreadID, file.RolloutID)
	}
	if err := guard.acquire(ids...); err != nil {
		return Result{}, err
	}
	choices, err := in.checkConflicts(ctx, sqliteHome)
	if err != nil {
		return Result{}, err
	}
	var installed []publishedLink
	extended := 0
	defer func() {
		if resultErr != nil {
			for i := len(installed) - 1; i >= 0; i-- {
				resultErr = errors.Join(resultErr, installed[i].removeIfOwned())
			}
			if extended > 0 {
				resultErr = fmt.Errorf("%d inherited prefixes were compatibly extended before publication failed; their original bytes remain intact and retry is safe: %w", extended, resultErr)
			}
		}
		for _, link := range installed {
			link.parent.Close()
		}
	}()
	result := Result{ThreadID: in.Plan.ThreadID, Destination: in.home.Path}
	for i, file := range in.Plan.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		chosen := choices[i]
		for _, extension := range chosen.extensions {
			if err := in.extendPrefix(ctx, file, extension); err != nil {
				return Result{}, err
			}
			extended++
		}
		if !chosen.exists {
			parent, err := in.home.directory(filepath.Dir(chosen.path), true)
			if err != nil {
				return Result{}, err
			}
			staged, err := in.stage.regular(file.SourcePath)
			if err != nil {
				parent.Close()
				return Result{}, err
			}
			info, err := staged.Stat()
			staged.Close()
			if err != nil {
				parent.Close()
				return Result{}, err
			}
			base := filepath.Base(chosen.path)
			if err := unix.Linkat(int(in.stage.dir.Fd()), file.SourcePath, int(parent.Fd()), base, 0); err != nil {
				parent.Close()
				return Result{}, fmt.Errorf("install rollout without overwrite %s: %w", chosen.path, err)
			}
			installed = append(installed, publishedLink{parent: parent, base: base, info: info})
			if err := parent.Sync(); err != nil {
				return Result{}, fmt.Errorf("sync installed rollout %s: %w", chosen.path, err)
			}
		}
		result.Files = append(result.Files, InstalledFile{Path: chosen.path, RolloutID: file.RolloutID, Prefix: file.Prefix, Created: !chosen.exists, Extended: len(chosen.extensions) > 0})
		if !file.Prefix {
			result.MainPath = chosen.path
			result.Archived = strings.HasPrefix(chosen.path, "archived_sessions/")
			result.Metadata = file.Metadata
		}
	}
	return result, nil
}

type publishedLink struct {
	parent *os.File
	base   string
	info   os.FileInfo
}

func (link publishedLink) removeIfOwned() error {
	fd, err := unix.Openat(int(link.parent.Fd()), link.base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check publication cleanup %s: %w", link.base, err)
	}
	f := os.NewFile(uintptr(fd), link.base)
	info, err := f.Stat()
	f.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(link.info, info) {
		return fmt.Errorf("publication cleanup preserved a replaced file: %s", link.base)
	}
	if err := unix.Unlinkat(int(link.parent.Fd()), link.base, 0); err != nil {
		return err
	}
	return link.parent.Sync()
}

func conflict(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrConflict, fmt.Sprintf(format, args...))
}

func (in *Incoming) checkConflicts(ctx context.Context, sqliteHome *Home) ([]choice, error) {
	inventory, err := in.home.inventory(ctx)
	if err != nil {
		return nil, err
	}
	main := in.Plan.Files[len(in.Plan.Files)-1]
	selected, err := sqliteHome.selection(ctx, main.ThreadID)
	if err != nil {
		return nil, err
	}
	var selectedPath string
	if selected != nil {
		selectedPath, err = in.home.managedRelative(selected.path)
		if err != nil {
			return nil, err
		}
		if prerequisitePath(selectedPath) {
			return nil, conflict("thread %s currently selects an inherited prefix; no existing selection was changed", main.ThreadID)
		}
		c, ok := parseName(selectedPath)
		if !ok && selected.mode == "legacy" {
			c = candidate{path: selectedPath, threadID: main.ThreadID, rolloutID: main.ThreadID}
			ok = true
		}
		if !ok || c.threadID != main.ThreadID || c.rolloutID != main.RolloutID || selected.mode != main.Metadata.HistoryMode {
			return nil, conflict("thread %s selects a different rollout or history mode", main.ThreadID)
		}
		// Include an authoritative legacy noncanonical filename in conflict checks.
		present := false
		for _, item := range inventory {
			if strings.TrimSuffix(item.path, ".zst") == strings.TrimSuffix(selectedPath, ".zst") {
				present = true
			}
		}
		if !present {
			for _, name := range []string{strings.TrimSuffix(selectedPath, ".zst"), strings.TrimSuffix(selectedPath, ".zst") + ".zst"} {
				f, err := in.home.regular(name)
				if err == nil {
					f.Close()
					c.path = name
					inventory = append(inventory, c)
					present = true
				} else if !isMissing(err) {
					return nil, err
				}
			}
		}
		if !present {
			return nil, conflict("thread %s has a missing authoritative destination rollout", main.ThreadID)
		}
	}
	if selectedPath == "" {
		for _, c := range inventory {
			if c.threadID == main.ThreadID && !prerequisitePath(c.path) && c.rolloutID != main.RolloutID {
				return nil, conflict("thread %s already has a different rollout without authoritative selection", main.ThreadID)
			}
		}
	}
	choices := make([]choice, len(in.Plan.Files))
	for i, incoming := range in.Plan.Files {
		choices[i].path = incoming.Path
		for _, c := range inventory {
			if c.rolloutID != incoming.RolloutID {
				continue
			}
			if c.threadID != incoming.ThreadID {
				return nil, conflict("rollout %s belongs to another thread", incoming.RolloutID)
			}
			if !incoming.Prefix && prerequisitePath(c.path) {
				return nil, conflict("rollout %s already exists as a partial prerequisite", incoming.RolloutID)
			}
			var end *HistoryPosition
			if incoming.Prefix {
				end = in.Plan.Files[i+1].Metadata.HistoryBase
			}
			existing, extension, err := in.compareExisting(ctx, incoming, c, end)
			if err != nil {
				return nil, conflict("cannot validate existing rollout %s: %v", c.path, err)
			}
			if extension != nil {
				choices[i].extensions = append(choices[i].extensions, *extension)
			} else if incoming.SHA256 != existing.SHA256 || incoming.Bytes != existing.Bytes {
				return nil, conflict("rollout %s has different bytes at %s", incoming.RolloutID, c.path)
			}
			if !choices[i].exists || (!prerequisitePath(c.path) && prerequisitePath(choices[i].path)) || strings.TrimSuffix(c.path, ".zst") == strings.TrimSuffix(selectedPath, ".zst") {
				choices[i].path, choices[i].exists = c.path, true
			}
		}
	}
	return choices, nil
}
