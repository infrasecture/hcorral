package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// directory creates only missing components, with private permissions. Existing
// directories retain their ownership and modes; symlinks are never followed.
func (h *Home) directory(name string, create bool) (*os.File, error) {
	if !validRelative(name) {
		return nil, fmt.Errorf("invalid directory within Codex home: %q", name)
	}
	parent := h.dir
	for _, component := range strings.Split(name, string(filepath.Separator)) {
		if create {
			err := unix.Mkdirat(int(parent.Fd()), component, 0o700)
			if err == nil {
				// Persist each new directory's entry in its parent, not only
				// the eventual rollout entry in the innermost directory.
				err = parent.Sync()
			}
			if err != nil && !errors.Is(err, unix.EEXIST) {
				if parent != h.dir {
					parent.Close()
				}
				return nil, fmt.Errorf("create %s: %w", name, err)
			}
		}
		fd, err := unix.Openat(int(parent.Fd()), component, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if parent != h.dir {
			parent.Close()
		}
		if err != nil {
			return nil, fmt.Errorf("open directory %s: %w", name, err)
		}
		parent = os.NewFile(uintptr(fd), component)
	}
	return parent, nil
}

func (h *Home) staging(ctx context.Context) (*Incoming, error) {
	coordination, err := h.stagingCoordination(ctx)
	if err != nil {
		return nil, err
	}
	defer coordination.Close()
	if err := h.recoverStaging(ctx); err != nil {
		return nil, err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	name := stagingPrefix + hex.EncodeToString(nonce[:])
	if err := unix.Mkdirat(int(h.dir.Fd()), name, 0o700); err != nil {
		return nil, err
	}
	dir, err := h.directory(name, false)
	if err != nil {
		unix.Unlinkat(int(h.dir.Fd()), name, unix.AT_REMOVEDIR)
		return nil, err
	}
	stage := &Home{Path: filepath.Join(h.Path, name), dir: dir}
	lease, err := stage.open(stagingLease, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err == nil {
		err = unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if err == nil {
		// Persist the lease before any payload can be created. After a machine
		// failure, a lease-free directory can therefore only be empty.
		err = errors.Join(lease.Sync(), dir.Sync(), h.dir.Sync())
	}
	if err != nil {
		if lease != nil {
			lease.Close()
		}
		unix.Unlinkat(int(dir.Fd()), stagingLease, 0)
		dir.Close()
		unix.Unlinkat(int(h.dir.Fd()), name, unix.AT_REMOVEDIR)
		return nil, fmt.Errorf("reserve transfer staging: %w", err)
	}
	// The coordinator remains held until the lease is locked, so recovery
	// cannot mistake a newly created, not-yet-locked directory for an orphan.
	return &Incoming{home: h, stage: stage, name: name, lease: lease}, nil
}

// Incoming owns private, verified staging files. Close removes only this
// transfer's staging area; it never removes installed or preexisting rollouts.
type Incoming struct {
	Plan        Plan
	home, stage *Home
	name        string
	slots       []string
	limits      Limits
	lease       *os.File
}

func (in *Incoming) Close() error {
	if in.stage == nil {
		return nil
	}
	// Cleanup must remain possible after the transfer context was cancelled.
	// If coordination cannot be acquired, release our lease and leave an orphan
	// for the next attempt instead of deleting without coordination.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	coordination, result := in.home.stagingCoordination(ctx)
	if result == nil {
		// The coordinator now excludes recovery, so the per-stage lease can
		// close before unlink. FUSE may retain an open unlinked lease as a
		// hidden file, preventing removal of the otherwise empty directory.
		result = in.lease.Close()
		in.lease = nil
		if result == nil {
			result = in.home.removeStaging(ctx, in.stage, in.name)
		}
		result = errors.Join(result, coordination.Close())
	}
	if in.lease != nil {
		result = errors.Join(result, in.lease.Close())
		in.lease = nil
	}
	result = errors.Join(result, in.stage.Close())
	in.stage = nil
	return result
}
