package session

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// This namespace defines a staging ownership protocol, separate from the wire
// format. Unversioned development staging and future versions are never reaped.
const stagingPrefix = ".hcorral-transfer-v1-"
const stagingLease = ".lease"
const stagingCoordinator = ".hcorral-staging.lock"

func (h *Home) stagingCoordination(ctx context.Context) (*os.File, error) {
	f, err := h.open(stagingCoordinator, unix.O_RDWR|unix.O_CREAT, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Sys().(*syscall.Stat_t).Nlink != 1 {
		f.Close()
		return nil, errors.New("transfer staging coordinator must be a regular, unshared lock file")
	}
	for {
		if err := ctx.Err(); err != nil {
			f.Close()
			return nil, err
		}
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("coordinate transfer staging: %w", err)
		}
		timer := time.NewTimer(25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
}

func stagingName(name string) bool {
	if !strings.HasPrefix(name, stagingPrefix) {
		return false
	}
	suffix := strings.TrimPrefix(name, stagingPrefix)
	nonce, err := hex.DecodeString(suffix)
	return err == nil && len(nonce) == 16 && suffix == strings.ToLower(suffix)
}

func stagingSlot(name string) bool {
	digits := ""
	switch {
	case strings.HasSuffix(name, ".jsonl"):
		digits = strings.TrimSuffix(name, ".jsonl")
	case strings.HasPrefix(name, "extension-"):
		digits = strings.TrimPrefix(name, "extension-")
	}
	if len(digits) < 6 || len(digits) > 19 {
		return false
	}
	for _, digit := range digits {
		if digit < '0' || digit > '9' {
			return false
		}
	}
	return true
}

// Walk only this directory, using a fresh descriptor so repeated validation and
// removal passes do not share an enumeration offset. No recursion or file reads.
func (h *Home) stagingEntries(ctx context.Context, visit func(os.DirEntry) error) error {
	fd, err := unix.Openat(int(h.dir.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), h.Path)
	defer dir.Close()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := dir.ReadDir(128)
		if err != nil && err != io.EOF {
			return err
		}
		for _, entry := range entries {
			if err := visit(entry); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

var errUnfamiliarStaging = errors.New("preserved unfamiliar transfer staging")

// The caller holds the coordinator and has established ownership or acquired
// an abandoned stage's lease, then closed it while still holding coordination.
// Validate the entire flat layout before unlinking anything. A staged hard link
// can already have a published sibling; unlinking only this name preserves it.
func (h *Home) removeStaging(ctx context.Context, stage *Home, name string) error {
	info, err := stage.dir.Stat()
	if err != nil {
		return err
	}
	current, err := h.directory(name, false)
	if err != nil {
		return err
	}
	after, err := current.Stat()
	current.Close()
	if err != nil || !os.SameFile(info, after) {
		return errors.New("staging directory changed identity before cleanup")
	}
	hasLease, slots := false, false
	err = stage.stagingEntries(ctx, func(entry os.DirEntry) error {
		if entry.Name() != stagingLease && !stagingSlot(entry.Name()) {
			return errUnfamiliarStaging
		}
		file, err := stage.regular(entry.Name())
		if err != nil {
			return fmt.Errorf("%w: %v", errUnfamiliarStaging, err)
		}
		info, err := file.Stat()
		file.Close()
		if err != nil {
			return err
		}
		if entry.Name() == stagingLease {
			hasLease = true
			if info.Size() != 0 || info.Sys().(*syscall.Stat_t).Nlink != 1 {
				return errUnfamiliarStaging
			}
		} else {
			slots = true
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The only legitimate lease-free state is a crash during directory
	// creation or after all payloads were removed during close/recovery.
	if slots && !hasLease {
		return errUnfamiliarStaging
	}
	err = stage.stagingEntries(ctx, func(entry os.DirEntry) error {
		if entry.Name() == stagingLease {
			return nil
		}
		return unix.Unlinkat(int(stage.dir.Fd()), entry.Name(), 0)
	})
	if err != nil {
		return err
	}
	if err := stage.stagingEntries(ctx, func(entry os.DirEntry) error {
		if entry.Name() != stagingLease {
			return errors.New("staging cleanup left entries; retained lease for retry")
		}
		return nil
	}); err != nil {
		return err
	}
	// Persist payload removal before removing the lease. Without this barrier,
	// a machine failure could leave payloads with no ownership-protocol marker.
	if err := stage.dir.Sync(); err != nil {
		return err
	}
	if hasLease {
		if err := unix.Unlinkat(int(stage.dir.Fd()), stagingLease, 0); err != nil {
			return err
		}
	}
	if err := stage.dir.Sync(); err != nil {
		return err
	}
	if err := unix.Unlinkat(int(h.dir.Fd()), name, unix.AT_REMOVEDIR); err != nil {
		return fmt.Errorf("remove transfer staging directory: %w", err)
	}
	return h.dir.Sync()
}

// Caller holds the coordinator. A crashed process releases its kernel lease;
// an arbitrarily old but live transfer retains it and is always skipped.
func (h *Home) recoverStaging(ctx context.Context) error {
	return h.stagingEntries(ctx, func(entry os.DirEntry) error {
		if !stagingName(entry.Name()) {
			return nil
		}
		// Inspect ownership without opening the private directory. A different
		// UID's inaccessible staging must not prevent our own import.
		var info unix.Stat_t
		if err := unix.Fstatat(int(h.dir.Fd()), entry.Name(), &info, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			return err
		}
		if info.Mode&unix.S_IFMT != unix.S_IFDIR || info.Mode&0o777 != 0o700 || info.Uid != uint32(os.Geteuid()) {
			return nil
		}
		return h.recoverStage(ctx, entry.Name())
	})
}

func (h *Home) recoverStage(ctx context.Context, name string) error {
	dir, err := h.directory(name, false)
	if err != nil {
		if errors.Is(err, unix.EACCES) || errors.Is(err, unix.EPERM) {
			return nil
		}
		return err
	}
	defer dir.Close()
	info, err := dir.Stat()
	if err != nil {
		return err
	}
	if info.Mode().Perm() != 0o700 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return nil
	}
	stage := &Home{Path: filepath.Join(h.Path, name), dir: dir}
	lease, err := stage.open(stagingLease, unix.O_RDWR, 0)
	if err != nil && !isMissing(err) {
		return nil // Symlinks, special files and inaccessible leases are preserved.
	}
	if lease != nil {
		defer func() {
			if lease != nil {
				lease.Close()
			}
		}()
		info, err := lease.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() != 0 || info.Sys().(*syscall.Stat_t).Nlink != 1 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return nil
		}
		if err := unix.Flock(int(lease.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
			if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
				return nil
			}
			return err
		}
		// Recovery has proved the prior owner is gone. Keep the coordinator
		// held, but close the file before unlink so FUSE need not hide it.
		err = lease.Close()
		lease = nil
		if err != nil {
			return err
		}
	}
	if err := h.removeStaging(ctx, stage, name); errors.Is(err, errUnfamiliarStaging) {
		return nil
	} else {
		return err
	}
}
