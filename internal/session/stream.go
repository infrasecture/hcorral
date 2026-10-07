package session

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sys/unix"
)

const ProtocolVersion = 1

type manifest struct {
	Version  int        `json:"version"`
	ThreadID string     `json:"thread_id"`
	Files    []wireFile `json:"files"`
}

// No source absolute paths, database contents, ownership or execution metadata
// are transported in the manifest. Native metadata is validated from payloads.
type wireFile struct {
	Path      string    `json:"path"`
	ThreadID  string    `json:"thread_id"`
	RolloutID string    `json:"rollout_id"`
	Bytes     int64     `json:"bytes"`
	SHA256    string    `json:"sha256"`
	Modified  time.Time `json:"modified"`
	Prefix    bool      `json:"prefix"`
}

func payloadName(i int) string { return fmt.Sprintf("files/%06d.jsonl", i) }

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

type countedReader struct {
	reader io.Reader
	bytes  int64
}

func (r *countedReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.bytes += int64(n)
	return n, err
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Export sends exactly the saved prefix captured by Snapshot. Later appends do
// not change the copy, and a rewritten prefix cannot produce a completed stream.
// Callers own cancellation of blocking pipes; no background goroutine is left
// reading an arbitrary io.Reader after cancellation.
func (s *Snapshot) Export(ctx context.Context, output io.Writer) error {
	if len(s.Plan.Files) == 0 || len(s.files) == 0 {
		return errors.New("session snapshot is empty or closed")
	}
	m := manifest{Version: ProtocolVersion, ThreadID: s.Plan.ThreadID}
	for _, f := range s.Plan.Files {
		if s.files[f.SourcePath] == nil {
			return errors.New("snapshot no longer owns all required source files")
		}
		path, err := publicationPath(f)
		if err != nil {
			return err
		}
		m.Files = append(m.Files, wireFile{Path: path, ThreadID: f.ThreadID, RolloutID: f.RolloutID, Bytes: f.Bytes, SHA256: f.SHA256, Modified: f.Modified, Prefix: f.Prefix})
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if int64(len(encoded)) > s.limits.ManifestBytes {
		return errors.New("session manifest exceeds configured limit")
	}
	w := tar.NewWriter(output)
	if err := writeMember(w, "manifest.json", encoded); err != nil {
		return err
	}
	for i, f := range s.Plan.Files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.WriteHeader(&tar.Header{Name: payloadName(i), Typeflag: tar.TypeReg, Mode: 0o600, Size: f.Bytes, Format: tar.FormatPAX}); err != nil {
			return err
		}
		input := io.NewSectionReader(s.files[f.SourcePath], 0, f.stored.Size())
		if err := copyPayload(ctx, w, f, input, strings.HasSuffix(f.SourcePath, ".zst") && !f.Prefix); err != nil {
			return err
		}
	}
	// No completion record is sent unless every source size/hash was verified.
	digest := sha256.Sum256(encoded)
	if err := writeMember(w, "complete.sha256", []byte(hex.EncodeToString(digest[:])+"\n")); err != nil {
		return err
	}
	return w.Close()
}

func writeMember(w *tar.Writer, name string, data []byte) error {
	if err := w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(data))}); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

func (h *Home) copyPayload(ctx context.Context, output io.Writer, file File) error {
	f, err := h.regular(file.SourcePath)
	if err != nil {
		return err
	}
	defer f.Close()
	return copyPayload(ctx, output, file, f, !file.Prefix)
}

func copyPayload(ctx context.Context, output io.Writer, file File, input io.Reader, exact bool) error {
	input = contextReader{ctx, input}
	if strings.HasSuffix(file.SourcePath, ".zst") {
		d, err := zstd.NewReader(input, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return err
		}
		defer d.Close()
		input = d
	}
	digest := sha256.New()
	if _, err := io.CopyN(io.MultiWriter(output, digest), contextReader{ctx, input}, file.Bytes); err != nil {
		return fmt.Errorf("stream %s: %w", file.SourcePath, err)
	}
	if hex.EncodeToString(digest.Sum(nil)) != file.SHA256 {
		return fmt.Errorf("source rollout changed after inspection: %s", file.SourcePath)
	}
	if exact {
		var extra [1]byte
		if n, err := input.Read(extra[:]); n != 0 || err != io.EOF {
			return fmt.Errorf("source rollout length or compressed trailer changed: %s (%v)", file.SourcePath, err)
		}
	}
	return nil
}

func expectedMember(r *tar.Reader, name string, size int64) error {
	header, err := r.Next()
	if err != nil {
		return fmt.Errorf("read transfer member %s: %w", name, err)
	}
	if header.Name != name || header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Size != size {
		return fmt.Errorf("unexpected transfer member: %q (expected regular %s, %d bytes)", header.Name, name, size)
	}
	return nil
}

// Receive never publishes live files. A valid completion record and full
// validation are required before it returns an Incoming for explicit publication.
func (h *Home) Receive(ctx context.Context, input io.Reader, limits Limits) (_ *Incoming, resultErr error) {
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	reader := &countedReader{reader: contextReader{ctx, input}}
	r := tar.NewReader(reader)
	header, err := r.Next()
	if err != nil {
		return nil, fmt.Errorf("read session manifest: %w", err)
	}
	if header.Name != "manifest.json" || header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Size < 1 || header.Size > limits.ManifestBytes {
		return nil, errors.New("invalid session manifest member or size")
	}
	encoded, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var m manifest
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		return nil, fmt.Errorf("invalid session manifest: %w", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("unexpected content after session manifest")
	}
	if err := m.validate(limits); err != nil {
		return nil, err
	}
	in, err := h.staging(ctx)
	if err != nil {
		return nil, fmt.Errorf("stage transfer in destination: %w", err)
	}
	in.limits, in.Plan = limits, Plan{ThreadID: m.ThreadID}
	stage := in.stage
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, in.Close())
		}
	}()
	for i, entry := range m.Files {
		if err := expectedMember(r, payloadName(i), entry.Bytes); err != nil {
			return nil, err
		}
		slot := fmt.Sprintf("%06d.jsonl", i)
		f, err := stage.open(slot, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0o600)
		if err != nil {
			return nil, err
		}
		in.slots = append(in.slots, slot)
		digest := sha256.New()
		_, copyErr := io.CopyN(io.MultiWriter(f, digest), r, entry.Bytes)
		if copyErr == nil && hex.EncodeToString(digest.Sum(nil)) != entry.SHA256 {
			copyErr = fmt.Errorf("checksum mismatch for rollout %s", entry.RolloutID)
		}
		if copyErr == nil {
			copyErr = f.Chmod(0o600)
		}
		if copyErr == nil {
			times := []unix.Timeval{unix.NsecToTimeval(entry.Modified.UnixNano()), unix.NsecToTimeval(entry.Modified.UnixNano())}
			copyErr = unix.Futimes(int(f.Fd()), times)
		}
		if copyErr == nil {
			copyErr = f.Sync()
		}
		copyErr = errors.Join(copyErr, f.Close())
		if copyErr != nil {
			return nil, copyErr
		}
		file, err := stage.readRollout(ctx, candidate{path: slot, threadID: entry.ThreadID, rolloutID: entry.RolloutID}, nil, limits)
		if err != nil {
			return nil, err
		}
		file.Path, file.Prefix = entry.Path, entry.Prefix
		if file.Bytes != entry.Bytes || file.SHA256 != entry.SHA256 {
			return nil, errors.New("staging content changed during validation")
		}
		wantPath, err := publicationPath(file)
		if err != nil {
			return nil, err
		}
		if wantPath != entry.Path {
			return nil, fmt.Errorf("noncanonical destination path in session manifest: %s", entry.Path)
		}
		in.Plan.Files = append(in.Plan.Files, file)
	}
	if err := in.validateLineage(); err != nil {
		return nil, err
	}
	if err := expectedMember(r, "complete.sha256", 65); err != nil {
		return nil, err
	}
	completion, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(encoded)
	if string(completion) != hex.EncodeToString(digest[:])+"\n" {
		return nil, errors.New("transfer completion checksum mismatch")
	}
	beforeTrailer := reader.bytes
	if _, err := r.Next(); err != io.EOF {
		return nil, fmt.Errorf("unexpected content after transfer completion: %v", err)
	}
	// archive/tar also accepts physical EOF without its end records. Our
	// protocol requires the generated completion padding and both end blocks.
	if reader.bytes-beforeTrailer != (512-65)+2*512 {
		return nil, errors.New("truncated session transfer trailer")
	}
	var extra [1]byte
	if n, err := reader.Read(extra[:]); n != 0 || err != io.EOF {
		return nil, fmt.Errorf("unexpected trailing transfer data: %v", err)
	}
	return in, nil
}

func (m manifest) validate(limits Limits) error {
	id, err := ParseID(m.ThreadID)
	if err != nil || id != m.ThreadID || m.Version != ProtocolVersion || len(m.Files) < 1 || len(m.Files) > limits.Files {
		return errors.New("invalid or unsupported session transfer manifest")
	}
	seen := make(map[string]bool)
	for i, f := range m.Files {
		thread, err := ParseID(f.ThreadID)
		if err != nil || thread != f.ThreadID {
			return errors.New("invalid thread identity in transfer manifest")
		}
		rollout, err := ParseID(f.RolloutID)
		if err != nil || rollout != f.RolloutID || seen[rollout] {
			return errors.New("invalid or repeated rollout identity in transfer manifest")
		}
		seen[rollout] = true
		checksum, err := hex.DecodeString(f.SHA256)
		if err != nil || len(checksum) != sha256.Size || f.SHA256 != strings.ToLower(f.SHA256) {
			return errors.New("invalid transfer checksum")
		}
		if !validRelative(f.Path) || (!strings.HasPrefix(f.Path, "sessions/") && !strings.HasPrefix(f.Path, "archived_sessions/")) || !strings.HasSuffix(f.Path, ".jsonl") {
			return fmt.Errorf("invalid transfer destination path: %q", f.Path)
		}
		if f.Bytes < 1 || f.Bytes > limits.FileBytes || f.Prefix != (i < len(m.Files)-1) || f.Modified.Year() < 1970 || f.Modified.Year() > 2261 {
			return errors.New("invalid transfer size, timestamp or prerequisite order")
		}
		if !f.Prefix && f.ThreadID != m.ThreadID {
			return errors.New("main rollout belongs to another thread")
		}
	}
	return nil
}

func (in *Incoming) validateLineage() error {
	for i, f := range in.Plan.Files {
		base := f.Metadata.HistoryBase
		if i == 0 {
			if base != nil {
				return errors.New("transfer omits an inherited rollout")
			}
			continue
		}
		parent := in.Plan.Files[i-1]
		if base == nil || base.RolloutID != parent.RolloutID || base.EndByteOffset != uint64(parent.Bytes) || parent.Metadata.HistoryMode != "paginated" || parent.lastOrdinal == math.MaxUint64 || parent.lastOrdinal+1 != base.EndOrdinalExclusive {
			return errors.New("transferred history dependencies or boundaries do not match")
		}
	}
	return nil
}
