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
	Timestamp   string           `json:"timestamp"`
	Version     string           `json:"cli_version"`
	CWD         string           `json:"cwd"`
	HistoryMode string           `json:"history_mode"`
	HistoryBase *HistoryPosition `json:"history_base"`
}

const prerequisiteRoot = "archived_sessions/.hcorral-history/"

func prerequisitePath(path string) bool { return strings.HasPrefix(path, prerequisiteRoot) }

// Native consumers search canonical filenames. Normalize their directory layout
// on publication without changing a single byte of the rollout contents.
func publicationPath(file File) (string, error) {
	name := filepath.Base(strings.TrimSuffix(file.Path, ".zst"))
	c, ok := parseName(name)
	if !ok {
		if file.Metadata.HistoryMode != "legacy" {
			return "", fmt.Errorf("paginated rollout requires a canonical filename: %s", file.Path)
		}
		stamp, err := time.Parse(time.RFC3339Nano, file.Metadata.Timestamp)
		if err != nil {
			return "", fmt.Errorf("legacy rollout has neither a canonical filename nor a usable timestamp")
		}
		name = "rollout-" + stamp.UTC().Format("2006-01-02T15-04-05") + "-" + file.ThreadID + ".jsonl"
		c, _ = parseName(name)
	}
	if c.threadID != file.ThreadID || c.rolloutID != file.RolloutID {
		return "", fmt.Errorf("rollout filename and manifest identity disagree: %s", file.Path)
	}
	if file.Prefix {
		return prerequisiteRoot + file.RolloutID + "/" + name, nil
	}
	if strings.HasPrefix(file.Path, "archived_sessions/") {
		return "archived_sessions/" + name, nil
	}
	date := strings.TrimPrefix(name, "rollout-")[:10]
	return "sessions/" + strings.ReplaceAll(date, "-", "/") + "/" + name, nil
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
