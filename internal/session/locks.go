package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("Codex session storage is busy")

const writerDirectory = "thread-writer-locks"

// Snapshot holds the Codex 0.160 writer locks for the selected thread and
// inherited rollouts. Keep it open through payload streaming, not only planning.
// This coordinates conforming writers; callers must qualify their Codex runtime
// against that protocol. It cannot coordinate arbitrary editors or older Codex.
type Snapshot struct {
	Plan  Plan
	guard *writerGuards
}

func (s *Snapshot) Close() error { return s.guard.Close() }

// Snapshot requires a writable lock namespace, even for export. Conversation
// files and the source SQLite database are only read. It never interrupts an
// active writer, waits for a running turn, or claims to flush that turn's data.
func (h *Home) Snapshot(ctx context.Context, threadID string, sqliteHome *Home, limits Limits) (*Snapshot, error) {
	id, err := ParseID(threadID)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := limits.validate(); err != nil {
		return nil, err
	}
	if sqliteHome == nil {
		return nil, fmt.Errorf("effective SQLite home is required for session selection")
	}
	guard := &writerGuards{home: h, files: make(map[string]*os.File)}
	if err := guard.acquire(id); err != nil {
		guard.Close()
		return nil, err
	}
	plan, err := h.inspect(ctx, id, sqliteHome, limits, func(c candidate) error {
		return guard.acquire(c.threadID, c.rolloutID)
	})
	if err != nil {
		guard.Close()
		return nil, err
	}
	return &Snapshot{Plan: plan, guard: guard}, nil
}

type writerGuards struct {
	home  *Home
	files map[string]*os.File
}

func (g *writerGuards) coordination() (*os.File, error) {
	if err := unix.Mkdirat(int(g.home.dir.Fd()), writerDirectory, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return nil, fmt.Errorf("create Codex writer-lock directory: %w", err)
	}
	return g.lockFile(writerDirectory + "/.coordination.lock")
}

func (g *writerGuards) lockFile(path string) (*os.File, error) {
	f, err := g.home.open(path, unix.O_RDWR|unix.O_CREAT, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open Codex writer lock: %w", err)
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		f.Close()
		return nil, fmt.Errorf("Codex writer lock is not a readable regular file: %s", path)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%w: %s; finish the owning Codex process before transfer", ErrBusy, path)
		}
		return nil, fmt.Errorf("acquire Codex writer lock %s: %w", path, err)
	}
	return f, nil
}

func (g *writerGuards) acquire(ids ...string) error {
	var pending []string
	for _, id := range ids {
		if g.files[id] == nil {
			pending = append(pending, id)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	coordination, err := g.coordination()
	if err != nil {
		return err
	}
	defer coordination.Close()
	sort.Strings(pending)
	for _, id := range pending {
		if g.files[id] != nil {
			continue
		}
		if _, err := ParseID(id); err != nil {
			return err
		}
		file, err := g.lockFile(writerDirectory + "/" + id + ".lock")
		if err != nil {
			return err
		}
		g.files[id] = file
	}
	return nil
}

func (g *writerGuards) Close() error {
	// Leave stale lock entries for Codex's own cleanup. Unlinking here would
	// require acquiring coordination during cancellation and risks breaking
	// another owner's inode identity. An unlocked entry is not a busy session.
	var result error
	for id, f := range g.files {
		result = errors.Join(result, f.Close())
		delete(g.files, id)
	}
	return result
}
