package session

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func fixtureTwoForks(t *testing.T, src *Home) (short, longer []byte) {
	t.Helper()
	short = fixtureBytes(t, threadA, "paginated", nil, "first inherited message")
	longer = fixtureBytes(t, threadA, "paginated", nil, "first inherited message", "second inherited message")
	parent := fixtureBytes(t, threadA, "paginated", nil, "first inherited message", "second inherited message", "private parent continuation")
	writeFixture(t, src, fixturePath(threadA, threadA), parent)
	for i, id := range []string{threadB, threadC} {
		prefix := [][]byte{short, longer}[i]
		base := &HistoryPosition{RolloutID: threadA, EndOrdinalExclusive: uint64(i + 2), EndByteOffset: uint64(len(prefix))}
		writeFixture(t, src, fixturePath(id, id), fixtureBytes(t, id, "paginated", base, "child "+id))
	}
	return short, longer
}

func TestTransferExtendsCompatibleManagedPrefixes(t *testing.T) {
	for _, representation := range []string{"plain", "compressed", "both"} {
		t.Run(representation, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			short, longer := fixtureTwoForks(t, src)
			first := published(t, received(t, dst, exported(t, src, threadB)))
			prefix := first.Files[0].Path
			reader, err := dst.regular(prefix)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			paths := []string{prefix}
			if representation != "plain" {
				writeFixture(t, dst, prefix+".zst", short)
				paths = append(paths, prefix+".zst")
			}
			if representation == "compressed" {
				if err := os.Remove(filepath.Join(dst.Path, prefix)); err != nil {
					t.Fatal(err)
				}
				paths = paths[1:]
			}
			originalChild, err := os.Stat(filepath.Join(dst.Path, first.MainPath))
			if err != nil {
				t.Fatal(err)
			}
			second := published(t, received(t, dst, exported(t, src, threadC)))
			if !second.Files[0].Extended || second.Files[0].Created || !second.Files[1].Created {
				t.Fatalf("extension was not reported: %+v", second)
			}
			for _, path := range paths {
				c, _ := parseName(path)
				file, err := dst.readRollout(context.Background(), c, nil, DefaultLimits())
				if err != nil || file.SHA256 != hash(longer) || file.Bytes != int64(len(longer)) {
					t.Fatalf("incomplete or excessive extension: %+v %v", file, err)
				}
				if file.stored.Mode().Perm() != 0o600 {
					t.Fatal("extension lost private permissions")
				}
			}
			// Readers already using the old prefix retain a complete snapshot.
			data, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(data, short) {
				t.Fatal("an open reader's prefix was modified in place")
			}
			for i, id := range []string{threadB, threadC} {
				plan := inspect(t, dst, id)
				if plan.Files[0].SHA256 != hash([][]byte{short, longer}[i]) {
					t.Fatalf("dependent %s reads the wrong inherited history", id)
				}
				// A later shorter import must not shorten the prerequisite again.
				repeated := published(t, received(t, dst, exported(t, src, id)))
				for _, file := range repeated.Files {
					if file.Created || file.Extended {
						t.Fatalf("repeat import mutated history: %+v", repeated)
					}
				}
			}
			after, _ := os.Stat(filepath.Join(dst.Path, first.MainPath))
			if !os.SameFile(originalChild, after) {
				t.Fatal("extension replaced an existing dependent")
			}
		})
	}
}

func TestPrefixExtensionChecksAllConflictsBeforeWriting(t *testing.T) {
	for _, conflictAt := range []string{"prefix", "main"} {
		t.Run(conflictAt, func(t *testing.T) {
			src, dst := fixtureHome(t), fixtureHome(t)
			fixtureTwoForks(t, src)
			first := published(t, received(t, dst, exported(t, src, threadB)))
			if conflictAt == "prefix" {
				writeFixture(t, dst, first.Files[0].Path, fixtureBytes(t, threadA, "paginated", nil, "independent content"))
			} else {
				writeFixture(t, dst, fixturePath(threadC, threadC), fixtureBytes(t, threadC, "paginated", nil, "independent content"))
			}
			path := filepath.Join(dst.Path, first.Files[0].Path)
			before, _ := os.ReadFile(path)
			inode, _ := os.Stat(path)
			in := received(t, dst, exported(t, src, threadC))
			if _, err := in.Publish(context.Background(), dst); !errors.Is(err, ErrConflict) {
				t.Fatalf("expected conflict: %v", err)
			}
			after, _ := os.ReadFile(path)
			newInode, _ := os.Stat(path)
			if !bytes.Equal(before, after) || !os.SameFile(inode, newInode) {
				t.Fatal("conflicting import changed an existing prerequisite")
			}
		})
	}
}

func TestPrefixExtensionPreservesPermissionsAndOwnership(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	fixtureTwoForks(t, src)
	first := published(t, received(t, dst, exported(t, src, threadB)))
	path := filepath.Join(dst.Path, first.Files[0].Path)
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group != os.Getegid() {
			if err := os.Chown(path, -1, group); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	published(t, received(t, dst, exported(t, src, threadC)))
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	oldStat, newStat := before.Sys().(*syscall.Stat_t), after.Sys().(*syscall.Stat_t)
	if oldStat.Uid != newStat.Uid || oldStat.Gid != newStat.Gid || before.Mode() != after.Mode() {
		t.Fatalf("extension changed permissions/ownership: before %v %d:%d, after %v %d:%d", before.Mode(), oldStat.Uid, oldStat.Gid, after.Mode(), newStat.Uid, newStat.Gid)
	}
}

func TestPrefixReuseDoesNotInspectUnneededTail(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	fixtureTwoForks(t, src)
	first := published(t, received(t, dst, exported(t, src, threadB)))
	path := first.Files[0].Path
	longer := fixtureBytes(t, threadA, "paginated", nil, "first inherited message", strings.Repeat("unneeded tail ", 1024))
	writeFixture(t, dst, path, longer)
	before, _ := os.Stat(filepath.Join(dst.Path, path))
	limits := DefaultLimits()
	limits.RecordBytes, limits.FileBytes = 2048, 4096
	in, err := dst.Receive(context.Background(), bytes.NewReader(exported(t, src, threadB)), limits)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	result := published(t, in)
	if result.Files[0].Created || result.Files[0].Extended {
		t.Fatal("shorter import did not reuse its existing prerequisite")
	}
	after, _ := os.Stat(filepath.Join(dst.Path, path))
	if !os.SameFile(before, after) || before.Size() != after.Size() {
		t.Fatal("shorter import changed a longer prerequisite")
	}
}

func TestPrefixExtensionSurvivesLaterFailureAndRetry(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	_, longer := fixtureTwoForks(t, src)
	initial := received(t, dst, exported(t, src, threadB))
	first := published(t, initial)
	if err := initial.Close(); err != nil {
		t.Fatal(err)
	}
	old := fixturePath(threadC, threadC)
	next := strings.ReplaceAll(strings.ReplaceAll(old, "2026/10/06", "2026/10/07"), "2026-10-06", "2026-10-07")
	data, _ := os.ReadFile(filepath.Join(src.Path, old))
	writeFixture(t, src, next, data)
	if err := os.Remove(filepath.Join(src.Path, old)); err != nil {
		t.Fatal(err)
	}
	block := filepath.Join(dst.Path, "sessions/2026/10/07")
	if err := os.WriteFile(block, []byte("existing directory blocker"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := received(t, dst, exported(t, src, threadC))
	if _, err := in.Publish(context.Background(), dst); err == nil || !strings.Contains(err.Error(), "compatibly extended") {
		t.Fatalf("missing partial-publication report: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst.Path, first.Files[0].Path))
	if err != nil || !bytes.Equal(got, longer) {
		t.Fatal("failed import damaged a complete compatible extension")
	}
	if err := os.Remove(block); err != nil {
		t.Fatal(err)
	}
	result := published(t, in)
	if result.Files[0].Extended || !result.Files[1].Created {
		t.Fatalf("retry did not reuse the verified extension: %+v", result)
	}
	if err := in.Close(); err != nil {
		t.Fatal(err)
	}
	assertNoStaging(t, dst)
}

func TestPrefixExtensionPreservesReplacedFile(t *testing.T) {
	src, dst := fixtureHome(t), fixtureHome(t)
	fixtureTwoForks(t, src)
	first := published(t, received(t, dst, exported(t, src, threadB)))
	in := received(t, dst, exported(t, src, threadC))
	choices, err := in.checkConflicts(context.Background(), dst)
	if err != nil || len(choices[0].extensions) != 1 {
		t.Fatalf("missing extension plan: %+v %v", choices, err)
	}
	path := filepath.Join(dst.Path, first.Files[0].Path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, dst, first.Files[0].Path, []byte("concurrent replacement"))
	if err := in.extendPrefix(context.Background(), in.Plan.Files[0], choices[0].extensions[0]); !errors.Is(err, ErrConflict) {
		t.Fatalf("did not detect replaced prerequisite: %v", err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != "concurrent replacement" {
		t.Fatal("extension overwrote a replaced prerequisite")
	}
}
