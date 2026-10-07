package session

import (
	"context"
	"errors"
	"os"
)

// Snapshot pins source files and their saved record boundaries. Codex may keep
// appending, rename/archive them, or replace them during migration. Export reads
// the pinned descriptors and verifies the captured hashes before completing.
// No source writer lock, staging directory, or writable source home is needed.
type Snapshot struct {
	Plan   Plan
	files  map[string]*os.File
	limits Limits
}

func (s *Snapshot) Close() error {
	var result error
	for path, f := range s.files {
		result = errors.Join(result, f.Close())
		delete(s.files, path)
	}
	return result
}

// Snapshot copies saved history without stopping or pausing the conversation.
// It does not flush another process's pending writes or wait for its current
// turn. The last unfinished JSONL record is excluded from the selected rollout;
// inherited history must still match its exact byte and ordinal boundaries.
func (h *Home) Snapshot(ctx context.Context, threadID string, sqliteHome *Home, limits Limits) (*Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &Snapshot{files: make(map[string]*os.File), limits: limits}
	plan, err := h.inspect(ctx, threadID, sqliteHome, limits, func(c candidate, end *HistoryPosition) (File, error) {
		f, err := h.regular(c.path)
		if err != nil {
			return File{}, err
		}
		s.files[c.path] = f
		return readRolloutFile(ctx, f, c, end, limits, true)
	})
	if err != nil {
		s.Close()
		return nil, err
	}
	s.Plan = plan
	return s, nil
}
