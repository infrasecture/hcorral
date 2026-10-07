package session

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func exported(t *testing.T, home *Home, id string) []byte {
	t.Helper()
	snapshot, err := home.Snapshot(context.Background(), id, home, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var stream bytes.Buffer
	if err := snapshot.Export(context.Background(), &stream); err != nil {
		t.Fatal(err)
	}
	return stream.Bytes()
}

func received(t *testing.T, home *Home, stream []byte) *Incoming {
	t.Helper()
	in, err := home.Receive(context.Background(), bytes.NewReader(stream), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := in.Close(); err != nil {
			t.Error(err)
		}
	})
	return in
}

func published(t *testing.T, in *Incoming) Result {
	t.Helper()
	result, err := in.Publish(context.Background(), in.home)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func fixtureFork(t *testing.T, src *Home) (parent, child, prefix []byte) {
	t.Helper()
	prefix = fixtureBytes(t, threadA, "paginated", nil, "inherited")
	parent = fixtureBytes(t, threadA, "paginated", nil, "inherited", "private parent continuation")
	writeFixture(t, src, fixturePath(threadA, threadA)+".zst", parent)
	base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: 2, EndByteOffset: uint64(len(prefix))}
	child = fixtureBytes(t, threadB, "paginated", base, "child")
	writeFixture(t, src, fixturePath(threadB, threadB), child)
	return
}

func assertNoStaging(t *testing.T, h *Home) {
	t.Helper()
	entries, err := os.ReadDir(h.Path)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".hcorral-transfer-") {
			t.Fatalf("staging leaked: %s", entry.Name())
		}
	}
}

func TestTransferStagesThenPublishesOnlySelectedHistory(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	_, child, prefix := fixtureFork(t, src)
	for _, path := range []string{"auth.json", "config.toml", "history.jsonl", "session_index.jsonl", "shell_snapshots/private"} {
		writeFixture(t, src, path, []byte("not conversation data"))
	}
	stamp := time.Unix(1700000000, 123456000)
	main := fixturePath(threadB, threadB)
	if err := os.Chtimes(filepath.Join(src.Path, main), stamp, stamp); err != nil {
		t.Fatal(err)
	}
	in := received(t, dst, exported(t, src, threadB))
	if _, err := os.Stat(filepath.Join(dst.Path, "sessions")); !os.IsNotExist(err) {
		t.Fatal("receive exposed a live session")
	}
	info, err := os.Stat(in.stage.Path)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("private stage mode: %v %v", info, err)
	}
	result := published(t, in)
	if result.ThreadID != threadB || result.Archived || result.MainPath != main || len(result.Files) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	for i, expected := range [][]byte{prefix, child} {
		file := result.Files[i]
		if !file.Created || file.Prefix != (i == 0) {
			t.Fatalf("wrong result file: %+v", file)
		}
		data, err := os.ReadFile(filepath.Join(dst.Path, file.Path))
		if err != nil || !bytes.Equal(data, expected) {
			t.Fatalf("wrong destination bytes for %s: %v", file.Path, err)
		}
		info, err := os.Stat(filepath.Join(dst.Path, file.Path))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("private file mode: %v %v", info, err)
		}
		if info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			t.Fatal("file was not owned by the receiving user")
		}
		if !file.Prefix && !info.ModTime().Equal(stamp) {
			t.Fatalf("mtime changed: %v", info.ModTime())
		}
	}
	if !strings.HasPrefix(result.Files[0].Path, prerequisiteRoot) {
		t.Fatal("prefix was published as a full active conversation")
	}
	for _, path := range []string{"auth.json", "config.toml", "history.jsonl", "session_index.jsonl", "shell_snapshots"} {
		if _, err := os.Stat(filepath.Join(dst.Path, path)); !os.IsNotExist(err) {
			t.Fatalf("copied excluded path %s", path)
		}
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, dst)
	// The copied file is independent of staging cleanup, and can be re-exported.
	again := inspect(t, dst, threadB)
	if again.Files[1].SHA256 != hash(child) || again.Files[0].SHA256 != hash(prefix) {
		t.Fatal("published lineage cannot be inspected")
	}
}

func TestRepeatTransferAndExistingArchiveAreNoOps(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	data := fixtureBytes(t, threadA, "legacy", nil, "same")
	writeFixture(t, src, fixturePath(threadA, threadA), data)
	archive := "archived_sessions/" + filepath.Base(fixturePath(threadA, threadA)) + ".zst"
	writeFixture(t, dst, archive, data)
	full := filepath.Join(dst.Path, archive)
	if err := os.Chmod(full, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		result := published(t, received(t, dst, exported(t, src, threadA)))
		if result.MainPath != archive || !result.Archived || result.Files[0].Created {
			t.Fatalf("not an identical no-op: %+v", result)
		}
	}
	after, err := os.Stat(full)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("identical existing rollout was changed")
	}
}

func TestTransferReusesFullAncestorWithoutTruncatingIt(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	parent, _, _ := fixtureFork(t, src)
	path := fixturePath(threadA, threadA)
	writeFixture(t, dst, path, parent)
	before, _ := os.Stat(filepath.Join(dst.Path, path))
	result := published(t, received(t, dst, exported(t, src, threadB)))
	if result.Files[0].Created || !result.Files[1].Created || result.Files[0].Path != path {
		t.Fatalf("did not reuse ancestor: %+v", result)
	}
	after, _ := os.Stat(filepath.Join(dst.Path, path))
	data, err := os.ReadFile(filepath.Join(dst.Path, path))
	if err != nil || !bytes.Equal(data, parent) || !os.SameFile(before, after) {
		t.Fatal("existing ancestor was shortened or replaced")
	}
}

func TestTransferConflictsBeforeAnyPublication(t *testing.T) {
	for _, kind := range []string{"main", "ancestor", "other rollout", "selected old rollout"} {
		t.Run(kind, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			fixtureFork(t, src)
			path := fixturePath(threadB, threadB)
			id := threadB
			if kind == "ancestor" {
				path, id = fixturePath(threadA, threadA), threadA
			}
			if kind == "other rollout" || kind == "selected old rollout" {
				path = fixturePath(threadB, rolloutA)
			}
			data := fixtureBytes(t, id, "paginated", nil, "destination's independent conversation")
			writeFixture(t, dst, path, data)
			if kind == "selected old rollout" {
				db := fixtureDB(t, dst, true)
				if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 0, 'paginated')", threadB, filepath.Join(dst.Path, path)); err != nil {
					t.Fatal(err)
				}
			}
			in := received(t, dst, exported(t, src, threadB))
			if _, err := in.Publish(context.Background(), dst); !errors.Is(err, ErrConflict) {
				t.Fatalf("got %v, want conflict", err)
			}
			inventory, err := dst.inventory(context.Background())
			if err != nil || len(inventory) != 1 {
				t.Fatalf("published files despite conflict: %v %v", inventory, err)
			}
			got, _ := os.ReadFile(filepath.Join(dst.Path, path))
			if !bytes.Equal(got, data) {
				t.Fatal("destination conversation changed")
			}
		})
	}
}

func TestInterruptedPublicationCleansOnlyItsNewFilesAndCanRetry(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	fixtureFork(t, src)
	block := filepath.Join(dst.Path, "sessions", "2026")
	if err := os.MkdirAll(filepath.Dir(block), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(block, []byte("preserve this preexisting blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := received(t, dst, exported(t, src, threadB))
	if _, err := in.Publish(context.Background(), dst); err == nil {
		t.Fatal("expected main-directory failure")
	}
	files, err := dst.inventory(context.Background())
	if err != nil || len(files) != 1 || !prerequisitePath(files[0].path) {
		t.Fatalf("partial publication did not retain its complete prerequisite: %+v %v", files, err)
	}
	if data, _ := os.ReadFile(block); string(data) != "preserve this preexisting blocker" {
		t.Fatal("cleanup removed preexisting content")
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	result := published(t, in)
	if len(result.Files) != 2 || result.Files[0].Created || !result.Files[1].Created {
		t.Fatal("retry did not complete")
	}
	parent, _ := os.Stat(filepath.Join(dst.Path, "sessions"))
	if parent.Mode().Perm() != 0o755 {
		t.Fatal("existing directory mode was changed")
	}
}

func TestPublishRefusesBusyDestination(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	writeFixture(t, src, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "legacy", nil, "one"))
	in := received(t, dst, exported(t, src, threadA))
	writer := &writerGuards{home: dst, files: make(map[string]*os.File)}
	if err := writer.acquire(threadA); err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := in.Publish(context.Background(), dst); !errors.Is(err, ErrBusy) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst.Path, "sessions")); !os.IsNotExist(err) {
		t.Fatal("busy publication created sessions")
	}
}

func rewriteStream(t *testing.T, stream []byte, change func(*tar.Header, []byte) (*tar.Header, []byte)) []byte {
	t.Helper()
	r := tar.NewReader(bytes.NewReader(stream))
	var output bytes.Buffer
	w := tar.NewWriter(&output)
	for {
		header, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatal(err)
		}
		header, data = change(header, data)
		if header == nil {
			continue
		}
		header.Size = int64(len(data))
		if err := w.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func TestReceiveRejectsCorruptUnexpectedAndIncompleteStreams(t *testing.T) {
	src := fixtureHome(t)
	writeFixture(t, src, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "legacy", nil, "original"))
	good := exported(t, src, threadA)
	for _, kind := range []string{"checksum", "missing completion", "symlink", "traversal", "future protocol", "missing trailer", "partial trailer", "trailing data"} {
		t.Run(kind, func(t *testing.T) {
			dst := fixtureHome(t)
			bad := rewriteStream(t, good, func(h *tar.Header, data []byte) (*tar.Header, []byte) {
				switch {
				case kind == "checksum" && h.Name == payloadName(0):
					data = bytes.Replace(data, []byte("original"), []byte("altered!"), 1)
				case kind == "missing completion" && h.Name == "complete.sha256":
					return nil, nil
				case kind == "symlink" && h.Name == payloadName(0):
					h.Typeflag = tar.TypeSymlink
					h.Linkname = "/tmp/escape"
					data = nil
				case kind == "traversal" && h.Name == payloadName(0):
					h.Name = "../auth.json"
				case kind == "future protocol" && h.Name == "manifest.json":
					var m manifest
					if err := json.Unmarshal(data, &m); err != nil {
						t.Fatal(err)
					}
					m.Version++
					data, _ = json.Marshal(m)
				}
				return h, data
			})
			switch kind {
			case "missing trailer":
				bad = bad[:len(bad)-1024]
			case "partial trailer":
				bad = bad[:len(bad)-1]
			case "trailing data":
				bad = append(bad, 1)
			}
			if in, err := dst.Receive(context.Background(), bytes.NewReader(bad), DefaultLimits()); err == nil {
				in.Close()
				t.Fatal("accepted corrupt stream")
			}
			assertNoStaging(t, dst)
			entries, err := os.ReadDir(dst.Path)
			if err != nil || len(entries) > 1 || (len(entries) == 1 && entries[0].Name() != stagingCoordinator) {
				t.Fatalf("failed receive retained more than its coordination lock: %v %v", entries, err)
			}
		})
	}
}

func TestSourceChangeNeverSendsCompletion(t *testing.T) {
	for _, kind := range []string{"append", "replace"} {
		t.Run(kind, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			path := fixturePath(threadA, threadA)
			data := fixtureBytes(t, threadA, "legacy", nil, "original")
			writeFixture(t, src, path, data)
			snapshot, err := src.Snapshot(context.Background(), threadA, src, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			defer snapshot.Close()
			if kind == "append" {
				data = append(data, []byte("{}\n")...)
			} else {
				data = bytes.Replace(data, []byte("original"), []byte("modified"), 1)
			}
			writeFixture(t, src, path, data)
			var wire bytes.Buffer
			if err := snapshot.Export(context.Background(), &wire); err == nil {
				t.Fatal("source mutation was not detected")
			}
			if in, err := dst.Receive(context.Background(), &wire, DefaultLimits()); err == nil {
				in.Close()
				t.Fatal("incomplete source was accepted")
			}
			assertNoStaging(t, dst)
		})
	}
}

func TestPublicationCleanupPreservesReplacement(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	fixtureFork(t, src)
	in := received(t, dst, exported(t, src, threadB))
	path := filepath.Join(dst.Path, in.Plan.Files[0].Path)
	interrupted := errors.New("interrupted after first file")
	_, err := in.publish(context.Background(), dst, func(i int) error {
		if i != 0 {
			t.Fatal("publication continued after interruption")
		}
		if err := os.Rename(path, path+".old"); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("someone else's file"), 0o600); err != nil {
			t.Fatal(err)
		}
		return interrupted
	})
	if !errors.Is(err, interrupted) || !strings.Contains(err.Error(), "remain at the destination") {
		t.Fatalf("missing retained-publication error: %v", err)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "someone else's file" {
		t.Fatal("cleanup deleted another file")
	}
}

func TestReceiveCreatesNoSymlinkedDestinationWrites(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	writeFixture(t, src, fixturePath(threadA, threadA), fixtureBytes(t, threadA, "legacy", nil, "one"))
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dst.Path, "sessions")); err != nil {
		t.Fatal(err)
	}
	in := received(t, dst, exported(t, src, threadA))
	if _, err := in.Publish(context.Background(), dst); err == nil {
		t.Fatal("followed destination symlink")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatal("wrote outside destination")
	}
}

func TestImportedRevertCanBeReexportedWithoutSQLite(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	prefix := fixtureBytes(t, threadA, "paginated", nil, "before revert")
	writeFixture(t, src, fixturePath(threadA, threadA), prefix)
	base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: 2, EndByteOffset: uint64(len(prefix))}
	main := fixturePath(threadA, rolloutA)
	writeFixture(t, src, main, fixtureBytes(t, threadA, "paginated", base, "reverted"))
	db := fixtureDB(t, src, true)
	if _, err := db.Exec("INSERT INTO threads VALUES (?, ?, 0, 'paginated')", threadA, main); err != nil {
		t.Fatal(err)
	}
	published(t, received(t, dst, exported(t, src, threadA)))
	plan := inspect(t, dst, threadA)
	if len(plan.Files) != 2 || plan.Files[1].RolloutID != rolloutA {
		t.Fatalf("re-export selected the prefix: %+v", plan)
	}
	if err := filepath.WalkDir(dst.Path, func(path string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".sqlite") {
			t.Fatal("publication created a database")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
