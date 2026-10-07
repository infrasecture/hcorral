package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// Plan describes validated history. Snapshot also pins the source descriptors
// so an export remains valid across appends and atomic path replacements.
// Files are ordered prerequisites first, requested conversation last.
type Plan struct {
	ThreadID string `json:"thread_id"`
	Files    []File `json:"files"`
}

// Inspect resolves persisted history without modifying conversation data.
// sqliteHome must be the caller's resolved effective SQLite home (which may
// differ from h); nil is rejected to avoid silently ignoring relocated state.
func (h *Home) Inspect(ctx context.Context, threadID string, sqliteHome *Home, limits Limits) (Plan, error) {
	return h.inspect(ctx, threadID, sqliteHome, limits, nil)
}

func (h *Home) inspect(ctx context.Context, threadID string, sqliteHome *Home, limits Limits, capture func(candidate, *HistoryPosition) (File, error)) (Plan, error) {
	id, err := ParseID(threadID)
	if err != nil {
		return Plan{}, err
	}
	if err := limits.Validate(); err != nil {
		return Plan{}, err
	}
	if sqliteHome == nil {
		return Plan{}, fmt.Errorf("effective SQLite home is required for session selection")
	}
	selected, err := sqliteHome.selection(ctx, id)
	if err != nil {
		return Plan{}, err
	}
	inventory, err := h.inventory(ctx)
	if err != nil {
		return Plan{}, err
	}
	var main []candidate
	if selected != nil {
		path, err := h.managedRelative(selected.path)
		if err != nil {
			return Plan{}, err
		}
		if prerequisitePath(path) {
			return Plan{}, fmt.Errorf("thread %s selects only an inherited history prefix, not a complete conversation", id)
		}
		for _, c := range inventory {
			if strings.TrimSuffix(c.path, ".zst") == strings.TrimSuffix(path, ".zst") {
				main = append(main, c)
			}
		}
		if len(main) == 0 && selected.mode == "legacy" {
			// Legacy SQLite can identify a noncanonical filename. Paginated
			// history must have a filename carrying its immutable rollout ID.
			for _, name := range []string{strings.TrimSuffix(path, ".zst"), strings.TrimSuffix(path, ".zst") + ".zst"} {
				f, err := h.regular(name)
				if err == nil {
					f.Close()
					main = append(main, candidate{path: name, threadID: id, rolloutID: id})
				} else if !isMissing(err) {
					return Plan{}, err
				}
			}
		}
		if len(main) == 0 {
			return Plan{}, fmt.Errorf("selected rollout for thread %s is missing: %s", id, selected.path)
		}
	} else {
		for _, c := range inventory {
			if c.threadID == id && !prerequisitePath(c.path) {
				main = append(main, c)
			}
		}
	}
	if len(main) == 0 {
		return Plan{}, fmt.Errorf("session %s was not found in the selected Codex home", id)
	}
	for _, c := range main {
		if c.threadID != id {
			return Plan{}, fmt.Errorf("selected rollout belongs to another thread: %s", c.path)
		}
		if c.rolloutID != main[0].rolloutID {
			return Plan{}, fmt.Errorf("ambiguous session %s: several rollouts exist without an authoritative SQLite selection", id)
		}
	}
	result := Plan{ThreadID: id}
	next := main
	var end *HistoryPosition
	seen := make(map[string]bool)
	for {
		if len(result.Files) >= limits.Files {
			return Plan{}, fmt.Errorf("session lineage exceeds the configured file limit")
		}
		rolloutID := next[0].rolloutID
		if seen[rolloutID] {
			return Plan{}, fmt.Errorf("cycle in inherited history at rollout %s", rolloutID)
		}
		seen[rolloutID] = true
		var chosen File
		for i, c := range next {
			var file File
			if capture != nil {
				file, err = capture(c, end)
			} else {
				file, err = h.readRollout(ctx, c, end, limits)
			}
			if err != nil {
				return Plan{}, err
			}
			if i == 0 {
				chosen = file
			} else if file.Bytes != chosen.Bytes || file.SHA256 != chosen.SHA256 || file.ThreadID != chosen.ThreadID {
				return Plan{}, fmt.Errorf("conflicting representations of rollout %s", rolloutID)
			}
		}
		if len(result.Files) == 0 && selected != nil {
			if selected.mode != chosen.Metadata.HistoryMode {
				return Plan{}, fmt.Errorf("SQLite and rollout history modes disagree for thread %s", id)
			}
			archived := strings.HasPrefix(chosen.SourcePath, "archived_sessions/")
			if selected.archived != archived {
				return Plan{}, fmt.Errorf("SQLite and rollout archive status disagree for thread %s", id)
			}
		}
		result.Files = append(result.Files, chosen)
		end = chosen.Metadata.HistoryBase
		if end == nil {
			break
		}
		next = nil
		for _, c := range inventory {
			if c.rolloutID == end.RolloutID {
				next = append(next, c)
			}
		}
		if len(next) == 0 {
			return Plan{}, fmt.Errorf("missing inherited rollout %s required by %s", end.RolloutID, chosen.RolloutID)
		}
	}
	for i, j := 0, len(result.Files)-1; i < j; i, j = i+1, j-1 {
		result.Files[i], result.Files[j] = result.Files[j], result.Files[i]
	}
	if capture != nil {
		// A revert or migration can select another rollout while we resolve
		// its lineage. Never silently combine that new selection with old data.
		after, err := sqliteHome.selection(ctx, id)
		if err != nil {
			return Plan{}, err
		}
		if (selected == nil) != (after == nil) || (selected != nil && *selected != *after) {
			return Plan{}, fmt.Errorf("conversation selection changed during snapshot; retry the copy")
		}
	}
	return result, nil
}

func (h *Home) managedRelative(path string) (string, error) {
	if filepath.IsAbs(path) {
		original := path
		for _, root := range []string{h.Path, h.logicalPath} {
			relative, err := filepath.Rel(root, original)
			if err == nil && validRelative(relative) {
				path = relative
				break
			}
		}
	}
	if !validRelative(path) || (!strings.HasPrefix(path, "sessions/") && !strings.HasPrefix(path, "archived_sessions/")) ||
		(!strings.HasSuffix(path, ".jsonl") && !strings.HasSuffix(path, ".jsonl.zst")) {
		return "", fmt.Errorf("selected rollout is outside managed session storage: %q", path)
	}
	return path, nil
}
