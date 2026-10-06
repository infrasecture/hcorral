package session

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ParseID accepts UUIDs only, not filename fragments, paths or tmux names.
func ParseID(value string) (string, error) {
	id := strings.ToLower(value)
	if !uuidPattern.MatchString(id) {
		return "", fmt.Errorf("session ID must be a complete UUID: %q", value)
	}
	return id, nil
}

type HistoryPosition struct {
	RolloutID           string `json:"thread_id"` // Historical wire name; this is NOT the stable thread ID.
	EndOrdinalExclusive uint64 `json:"end_ordinal_exclusive"`
	EndByteOffset       uint64 `json:"end_byte_offset"`
}

// Metadata contains only the fields needed for selection and validation. The
// payload is never serialized back over the original bytes.
type Metadata struct {
	ThreadID    string           `json:"id"`
	Version     string           `json:"cli_version"`
	CWD         string           `json:"cwd"`
	HistoryMode string           `json:"history_mode"`
	HistoryBase *HistoryPosition `json:"history_base"`
}

type candidate struct {
	path, threadID, rolloutID string
}

func parseName(path string) (candidate, bool) {
	name := strings.TrimSuffix(filepath.Base(path), ".zst")
	if !strings.HasPrefix(name, "rollout-") || !strings.HasSuffix(name, ".jsonl") {
		return candidate{}, false
	}
	core := strings.TrimSuffix(strings.TrimPrefix(name, "rollout-"), ".jsonl")
	if len(core) < 20 || core[19] != '-' {
		return candidate{}, false
	}
	if _, err := time.Parse("2006-01-02T15-04-05", core[:19]); err != nil {
		return candidate{}, false
	}
	thread, rollout, forked := strings.Cut(core[20:], "_")
	if !forked {
		rollout = thread
	}
	thread, err := ParseID(thread)
	if err != nil {
		return candidate{}, false
	}
	rollout, err = ParseID(rollout)
	if err != nil {
		return candidate{}, false
	}
	return candidate{path: path, threadID: thread, rolloutID: rollout}, true
}
