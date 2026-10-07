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
	Promoted  bool   `json:"promoted,omitempty"`
}

type Result struct {
	ThreadID          string          `json:"thread_id"`
	Destination       string          `json:"destination"`
	MainPath          string          `json:"main_path"`
	Archived          bool            `json:"archived"`
	Files             []InstalledFile `json:"files"`
	Metadata          Metadata        `json:"metadata"`
	SelectionRepaired bool            `json:"selection_repaired,omitempty"`
}

type choice struct {
	path       string
	exists     bool
	extensions []prefixExtension
	promoted   bool
	repair     *selection
}

// Publish validates every existing identity before installing any live file.
// New rollouts use exclusive hard links from same-filesystem staging.
// A managed prerequisite may grow atomically after its entire
// existing prefix is verified against the incoming bytes. Writer guards remain
// held through validation and publication.
func (in *Incoming) Publish(ctx context.Context, sqliteHome *Home) (_ Result, resultErr error) {
	return in.publish(ctx, sqliteHome, nil)
}

// afterFile is an internal test seam for native indexer interleavings and
// interruption at a durable publication boundary. Public callers cannot set it.
func (in *Incoming) publish(ctx context.Context, sqliteHome *Home, afterFile func(int) error) (_ Result, resultErr error) {
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
	prepared := make([][]preparedPrefix, len(choices))
	for i, chosen := range choices {
		for _, extension := range chosen.extensions {
			prefix, err := in.preparePrefix(ctx, in.Plan.Files[i], extension)
			if err != nil {
				return Result{}, err
			}
			prepared[i] = append(prepared[i], prefix)
		}
	}
	var update *selectionUpdate
	if expected := choices[len(choices)-1].repair; expected != nil {
		update, err = sqliteHome.beginSelectionUpdate(ctx, in.Plan.ThreadID, *expected)
		if err != nil {
			return Result{}, err
		}
		defer update.close()
	}
	installed := 0
	extended := 0
	defer func() {
		if resultErr != nil {
			if installed > 0 {
				resultErr = fmt.Errorf("%d validated history files remain at the destination after publication failed; retry verifies and reuses them: %w", installed, resultErr)
			}
			if extended > 0 {
				resultErr = fmt.Errorf("%d inherited prefixes were compatibly extended before publication failed; their original bytes remain intact and retry is safe: %w", extended, resultErr)
			}
		}
	}()
	result := Result{ThreadID: in.Plan.ThreadID, Destination: in.home.Path}
	for i, file := range in.Plan.Files {
		if err := ctx.Err(); err != nil {
			return Result{}, err
		}
		chosen := choices[i]
		for _, prefix := range prepared[i] {
			if err := in.publishPrefix(ctx, prefix); err != nil {
				return Result{}, err
			}
			extended++
		}
		if !chosen.exists {
			parent, err := in.home.directory(filepath.Dir(chosen.path), true)
			if err != nil {
				return Result{}, err
			}
			base := filepath.Base(chosen.path)
			if err := unix.Linkat(int(in.stage.dir.Fd()), file.SourcePath, int(parent.Fd()), base, 0); err != nil {
				parent.Close()
				return Result{}, fmt.Errorf("install rollout without overwrite %s: %w", chosen.path, err)
			}
			installed++
			syncErr := parent.Sync()
			parent.Close()
			if syncErr != nil {
				return Result{}, fmt.Errorf("sync installed rollout %s: %w", chosen.path, syncErr)
			}
		}
		promoted := chosen.promoted && (!chosen.exists || len(chosen.extensions) > 0 || chosen.repair != nil)
		result.Files = append(result.Files, InstalledFile{Path: chosen.path, RolloutID: file.RolloutID, Prefix: file.Prefix, Created: !chosen.exists, Extended: file.Prefix && len(chosen.extensions) > 0, Promoted: promoted})
		if !file.Prefix {
			result.MainPath = chosen.path
			result.Archived = strings.HasPrefix(chosen.path, "archived_sessions/")
			result.Metadata = file.Metadata
		}
		if afterFile != nil {
			if err := afterFile(i); err != nil {
				return Result{}, err
			}
		}
	}
	if update != nil {
		// A failed commit can have an unknown result. Keep the complete files
		// regardless; removing them might invalidate a committed selection.
		main := in.Plan.Files[len(in.Plan.Files)-1]
		if err := update.finish(ctx, filepath.Join(in.home.Path, result.MainPath), result.Archived, main.Modified); err != nil {
			return Result{}, fmt.Errorf("complete history files were installed at %q, but metadata promotion was not confirmed; retry to verify and finish: %w", result.MainPath, err)
		}
		result.SelectionRepaired = true
	}
	if len(in.Plan.Files) > 1 || choices[len(choices)-1].promoted {
		repaired, err := in.finishPublicationSelection(ctx, sqliteHome, result)
		if err != nil {
			return Result{}, fmt.Errorf("complete history files are installed, but Codex's selected conversation was not confirmed; retry to verify and finish: %w", err)
		}
		result.SelectionRepaired = result.SelectionRepaired || repaired
	}
	return result, nil
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
	var repair *selection
	if selected != nil {
		selectedPath, err = in.home.managedRelative(selected.path)
		if err != nil {
			return nil, err
		}
		c, ok := parseName(selectedPath)
		if !ok && selected.mode == "legacy" {
			c = candidate{path: selectedPath, threadID: main.ThreadID, rolloutID: main.ThreadID}
			ok = true
		}
		if prerequisitePath(selectedPath) {
			matches := false
			for _, file := range in.Plan.Files {
				if file.ThreadID == main.ThreadID && file.RolloutID == c.rolloutID {
					matches = true
				}
			}
			canonical := prerequisiteRoot + c.rolloutID + "/" + filepath.Base(strings.TrimSuffix(selectedPath, ".zst"))
			if !ok || !matches || selected.mode != "paginated" || main.Metadata.HistoryMode != "paginated" || strings.TrimSuffix(selectedPath, ".zst") != canonical {
				return nil, conflict("thread %s selects a prerequisite outside the validated incoming lineage", main.ThreadID)
			}
			repair = selected
		}
		if !ok || c.threadID != main.ThreadID || (repair == nil && c.rolloutID != main.RolloutID) || selected.mode != main.Metadata.HistoryMode {
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
	if selectedPath == "" || repair != nil {
		for _, c := range inventory {
			if c.threadID == main.ThreadID && !prerequisitePath(c.path) && c.rolloutID != main.RolloutID {
				return nil, conflict("thread %s already has a different rollout without authoritative selection", main.ThreadID)
			}
		}
	}
	choices := make([]choice, len(in.Plan.Files))
	for i, incoming := range in.Plan.Files {
		choices[i].path = incoming.Path
		if !incoming.Prefix {
			choices[i].repair = repair
		}
		for _, c := range inventory {
			if c.rolloutID != incoming.RolloutID {
				continue
			}
			if c.threadID != incoming.ThreadID {
				return nil, conflict("rollout %s belongs to another thread", incoming.RolloutID)
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
			if !incoming.Prefix && prerequisitePath(c.path) {
				// Keep the dependency path, but publish the complete parent at a
				// normal native location. It must not remain classified as partial.
				choices[i].promoted = true
				continue
			}
			if !choices[i].exists || (!prerequisitePath(c.path) && prerequisitePath(choices[i].path)) || strings.TrimSuffix(c.path, ".zst") == strings.TrimSuffix(selectedPath, ".zst") {
				choices[i].path, choices[i].exists = c.path, true
			}
		}
	}
	return choices, nil
}
