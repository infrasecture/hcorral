package session

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

// Limits bound a corrupt input's resource use. Callers can raise these for
// large legitimate conversations without changing the transfer format.
type Limits struct {
	RecordBytes   int64
	FileBytes     int64
	Files         int
	ManifestBytes int64
}

func DefaultLimits() Limits {
	return Limits{RecordBytes: 256 << 20, FileBytes: 64 << 30, Files: 4096, ManifestBytes: 8 << 20}
}

func (l Limits) Validate() error {
	if l.RecordBytes < 1 || l.FileBytes < 1 || l.RecordBytes > l.FileBytes || l.Files < 1 || l.ManifestBytes < 1 {
		return errors.New("session limits must be positive and record size must not exceed file size")
	}
	return nil
}

type File struct {
	SourcePath  string    `json:"source_path"`
	Path        string    `json:"path"` // Relative destination path; decoded JSONL.
	ThreadID    string    `json:"thread_id"`
	RolloutID   string    `json:"rollout_id"`
	Bytes       int64     `json:"bytes"`
	SHA256      string    `json:"sha256"`
	Modified    time.Time `json:"modified"`
	Prefix      bool      `json:"prefix"`
	Metadata    Metadata  `json:"metadata"`
	lastOrdinal uint64
	stored      os.FileInfo
}

type line struct {
	Type    string          `json:"type"`
	Ordinal *uint64         `json:"ordinal"`
	Payload json.RawMessage `json:"payload"`
}

// readRollout validates and hashes decoded bytes without reconstructing records.
// For an inherited prefix, it does not consume the parent's subsequent history.
func (h *Home) readRollout(ctx context.Context, c candidate, end *HistoryPosition, limits Limits) (File, error) {
	f, err := h.regular(c.path)
	if err != nil {
		return File{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	var input io.Reader = f
	if strings.HasSuffix(c.path, ".zst") {
		decoder, err := zstd.NewReader(f, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(256<<20))
		if err != nil {
			return File{}, fmt.Errorf("open compressed rollout %s: %w", c.path, err)
		}
		defer decoder.Close()
		input = decoder
	}
	if end != nil {
		if end.EndByteOffset == 0 || end.EndByteOffset > uint64(limits.FileBytes) || end.EndOrdinalExclusive == 0 {
			return File{}, fmt.Errorf("invalid history boundary for %s", c.path)
		}
		input = io.LimitReader(input, int64(end.EndByteOffset))
	}
	r := bufio.NewReaderSize(input, 64<<10)
	digest := sha256.New()
	result := File{SourcePath: c.path, Path: strings.TrimSuffix(c.path, ".zst"), ThreadID: c.threadID, RolloutID: c.rolloutID, Modified: before.ModTime(), Prefix: end != nil, stored: before}
	var previous uint64
	var records int64
	var sawMetadata bool
	for {
		if err := ctx.Err(); err != nil {
			return File{}, err
		}
		bytes, err := readRecord(ctx, r, limits.RecordBytes)
		if err == io.EOF && len(bytes) == 0 {
			break
		}
		if err != nil {
			return File{}, fmt.Errorf("read rollout %s record %d: %w", c.path, records+1, err)
		}
		if int64(len(bytes)) > limits.FileBytes-result.Bytes {
			return File{}, fmt.Errorf("rollout %s exceeds the configured file limit", c.path)
		}
		var record line
		if !utf8.Valid(bytes) {
			return File{}, fmt.Errorf("invalid UTF-8 in %s record %d", c.path, records+1)
		}
		if err := json.Unmarshal(bytes, &record); err != nil {
			return File{}, fmt.Errorf("invalid JSON in %s record %d: %w", c.path, records+1, err)
		}
		if record.Type == "" || len(record.Payload) == 0 {
			return File{}, fmt.Errorf("missing record type or payload in %s record %d", c.path, records+1)
		}
		if record.Type == "session_meta" && !sawMetadata {
			if err := json.Unmarshal(record.Payload, &result.Metadata); err != nil {
				return File{}, fmt.Errorf("invalid session metadata in %s: %w", c.path, err)
			}
			if err := validateMetadata(&result.Metadata, c); err != nil {
				return File{}, err
			}
			if result.Metadata.HistoryMode == "paginated" && records != 0 {
				return File{}, fmt.Errorf("paginated rollout %s does not begin with session metadata", c.path)
			}
			sawMetadata = true
		} else if record.Type == "session_meta" && result.Metadata.HistoryMode == "paginated" {
			return File{}, fmt.Errorf("repeated session metadata in %s", c.path)
		}
		if result.Metadata.HistoryMode == "paginated" {
			if record.Ordinal == nil {
				return File{}, fmt.Errorf("missing paginated ordinal in %s record %d", c.path, records+1)
			}
			if records == 0 {
				var initial uint64
				if base := result.Metadata.HistoryBase; base != nil {
					initial = base.EndOrdinalExclusive
				}
				if *record.Ordinal != initial {
					return File{}, fmt.Errorf("metadata ordinal disagrees with inherited history in %s", c.path)
				}
			} else if *record.Ordinal <= previous {
				return File{}, fmt.Errorf("non-increasing paginated ordinals in %s", c.path)
			}
			previous = *record.Ordinal
		}
		digest.Write(bytes)
		result.Bytes += int64(len(bytes))
		records++
	}
	if !sawMetadata {
		return File{}, fmt.Errorf("missing session metadata in rollout: %s", c.path)
	}
	if end != nil && (result.Metadata.HistoryMode != "paginated" || uint64(result.Bytes) != end.EndByteOffset || previous == math.MaxUint64 || previous+1 != end.EndOrdinalExclusive) {
		return File{}, fmt.Errorf("history byte and ordinal boundaries disagree in %s", c.path)
	}
	after, err := f.Stat()
	if err != nil {
		return File{}, err
	}
	if before.Size() != after.Size() || !before.ModTime().Equal(after.ModTime()) {
		return File{}, fmt.Errorf("rollout changed during inspection: %s", c.path)
	}
	result.SHA256 = hex.EncodeToString(digest.Sum(nil))
	result.lastOrdinal = previous
	return result, nil
}

func validateMetadata(meta *Metadata, c candidate) error {
	id, err := ParseID(meta.ThreadID)
	if err != nil || id != c.threadID {
		return fmt.Errorf("session metadata disagrees with filename identity in %s", c.path)
	}
	meta.ThreadID = id
	if meta.HistoryMode == "" {
		meta.HistoryMode = "legacy"
	}
	if meta.HistoryMode != "legacy" && meta.HistoryMode != "paginated" {
		return fmt.Errorf("unsupported history mode %q in %s", meta.HistoryMode, c.path)
	}
	if base := meta.HistoryBase; base != nil {
		id, err := ParseID(base.RolloutID)
		if err != nil || base.EndOrdinalExclusive == 0 || base.EndOrdinalExclusive == math.MaxUint64 || base.EndByteOffset == 0 || meta.HistoryMode != "paginated" {
			return fmt.Errorf("invalid inherited history in %s", c.path)
		}
		base.RolloutID = id
	}
	return nil
}

func readRecord(ctx context.Context, r *bufio.Reader, max int64) ([]byte, error) {
	var record []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		fragment, err := r.ReadSlice('\n')
		if int64(len(fragment)) > max-int64(len(record)) {
			return nil, errors.New("record exceeds configured limit")
		}
		record = append(record, fragment...)
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(record) != 0 {
			return nil, errors.New("incomplete final JSONL record")
		}
		return record, err
	}
}
