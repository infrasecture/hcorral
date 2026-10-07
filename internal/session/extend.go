package session

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

// Only hcorral's managed prerequisites can grow. A regular conversation is
// never shortened, appended to or replaced by a transfer. Keep every managed
// representation sufficient for the new cutoff: native Codex's lineage lookup
// can choose any matching rollout filename, including a compressed copy.
type prefixExtension struct {
	old File
}

func (in *Incoming) compareExisting(ctx context.Context, incoming File, c candidate, end *HistoryPosition) (File, *prefixExtension, error) {
	// Try the required cutoff first. A longer existing representation can be
	// reused without parsing or limiting the unrelated tail it already contains.
	existing, boundaryErr := in.home.readRollout(ctx, c, end, in.limits)
	if !prerequisitePath(c.path) || (incoming.Prefix && boundaryErr == nil) || (!incoming.Prefix && boundaryErr != nil) {
		return existing, nil, boundaryErr
	}
	if incoming.Prefix {
		var err error
		existing, err = in.home.readRollout(ctx, c, nil, in.limits)
		if err != nil {
			return File{}, nil, boundaryErr
		}
	}
	if existing.Bytes >= incoming.Bytes {
		if !incoming.Prefix {
			return existing, nil, nil
		}
		return File{}, nil, boundaryErr
	}
	if existing.Metadata.HistoryMode != "paginated" || existing.lastOrdinal == math.MaxUint64 {
		return File{}, nil, errors.New("managed prerequisite is not a valid paginated prefix")
	}
	cutoff := &HistoryPosition{RolloutID: incoming.RolloutID, EndByteOffset: uint64(existing.Bytes), EndOrdinalExclusive: existing.lastOrdinal + 1}
	staged, err := in.stage.readRollout(ctx, candidate{path: incoming.SourcePath, threadID: incoming.ThreadID, rolloutID: incoming.RolloutID}, cutoff, in.limits)
	if err != nil {
		return File{}, nil, err
	}
	if staged.SHA256 != existing.SHA256 || staged.Bytes != existing.Bytes {
		return File{}, nil, errors.New("incoming history diverges from the existing inherited prefix")
	}
	return existing, &prefixExtension{old: existing}, nil
}

// Publication replaces only a proven managed prefix with its byte-for-byte
// extension. Atomic rename leaves open readers of the old prefix undisturbed;
// future readers see the whole longer prefix, never a partly appended record.
// Writer guards cover cooperating native writers throughout this operation.
// Arbitrary concurrent filesystem edits do not participate in that contract.
type preparedPrefix struct {
	old  File
	slot string
}

func (in *Incoming) extendPrefix(ctx context.Context, incoming File, extension prefixExtension) error {
	prepared, err := in.preparePrefix(ctx, incoming, extension)
	if err != nil {
		return err
	}
	return in.publishPrefix(ctx, prepared)
}

func (in *Incoming) preparePrefix(ctx context.Context, incoming File, extension prefixExtension) (preparedPrefix, error) {
	old := extension.old
	if incoming.Metadata.HistoryMode != "paginated" || !prerequisitePath(old.SourcePath) || old.stored == nil {
		return preparedPrefix{}, errors.New("extension requires a validated managed prerequisite")
	}
	slot := fmt.Sprintf("extension-%06d", len(in.slots))
	f, err := in.stage.open(slot, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err != nil {
		return preparedPrefix{}, err
	}
	in.slots = append(in.slots, slot)
	var output io.Writer = f
	var encoder *zstd.Encoder
	if strings.HasSuffix(old.SourcePath, ".zst") {
		encoder, err = zstd.NewWriter(f, zstd.WithEncoderConcurrency(1))
		if err != nil {
			f.Close()
			return preparedPrefix{}, err
		}
		output = encoder
	}
	err = in.stage.copyPayload(ctx, output, incoming)
	if encoder != nil {
		err = errors.Join(err, encoder.Close())
	}
	if err == nil {
		// Preserve the existing prefix's ownership rather than silently changing
		// group access when the importing process has a different primary group.
		stat := old.stored.Sys().(*syscall.Stat_t)
		err = f.Chown(int(stat.Uid), int(stat.Gid))
	}
	if err == nil {
		err = f.Chmod(old.stored.Mode().Perm())
	}
	if err == nil {
		times := []unix.Timeval{unix.NsecToTimeval(incoming.Modified.UnixNano()), unix.NsecToTimeval(incoming.Modified.UnixNano())}
		err = unix.Futimes(int(f.Fd()), times)
	}
	if err == nil {
		err = f.Sync()
	}
	err = errors.Join(err, f.Close())
	if err != nil {
		return preparedPrefix{}, err
	}
	return preparedPrefix{old: old, slot: slot}, nil
}

func (in *Incoming) publishPrefix(ctx context.Context, prepared preparedPrefix) error {
	old := prepared.old
	parent, err := in.home.directory(filepath.Dir(old.SourcePath), false)
	if err != nil {
		return err
	}
	defer parent.Close()
	current, err := in.home.regular(old.SourcePath)
	if err != nil {
		return err
	}
	info, err := current.Stat()
	current.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(old.stored, info) || old.stored.Size() != info.Size() || !old.stored.ModTime().Equal(info.ModTime()) {
		return conflict("managed prerequisite changed before extension: %s", old.SourcePath)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := unix.Renameat(int(in.stage.dir.Fd()), prepared.slot, int(parent.Fd()), filepath.Base(old.SourcePath)); err != nil {
		return fmt.Errorf("extend inherited prefix %s: %w", old.SourcePath, err)
	}
	if err := parent.Sync(); err != nil {
		return fmt.Errorf("inherited prefix %s was extended but directory sync failed: %w", old.SourcePath, err)
	}
	return nil
}
