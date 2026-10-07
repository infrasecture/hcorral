package session

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestSharedFilesystemPolicy(t *testing.T) {
	for _, tc := range []struct {
		name  string
		local bool
		deny  bool
	}{
		{"apfs", true, false}, {"hfs", true, false},
		{"nfs", false, true}, {"smbfs", false, true},
		{"macfuse", true, true}, {"osxfuse", true, true},
		{"virtiofs", true, true}, {"9p", true, true}, {"", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var fs unix.Statfs_t
			copy(fs.Fstypename[:], tc.name)
			if tc.local {
				fs.Flags |= unix.MNT_LOCAL
			}
			if got := unsupportedFilesystem(&fs); (got != "") != tc.deny {
				t.Fatalf("filesystem rejection = %q; want deny=%v", got, tc.deny)
			}
		})
	}
}
