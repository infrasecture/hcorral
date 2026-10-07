package session

import (
	"errors"
	"fmt"
	"os"
	"sort"

	"golang.org/x/sys/unix"
)

var ErrBusy = errors.New("Codex session storage is busy")

const writerDirectory = "thread-writer-locks"

// writerGuards protect destination publication. Source snapshots deliberately
// do not acquire them: Codex owns a thread's lock for as long as it is loaded.
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
			return nil, fmt.Errorf("%w: destination %s is in use; existing conversations are never overwritten", ErrBusy, path)
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
