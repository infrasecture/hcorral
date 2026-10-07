package session

import (
	"testing"

	"golang.org/x/sys/unix"
)

func TestSharedFilesystemPolicy(t *testing.T) {
	for _, kind := range []int64{unix.FUSE_SUPER_MAGIC, unix.V9FS_MAGIC, unix.NFS_SUPER_MAGIC, unix.CIFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC} {
		if unsupportedFilesystem(&unix.Statfs_t{Type: kind}) == "" {
			t.Fatalf("accepted unqualified shared filesystem %#x", kind)
		}
	}
	for _, kind := range []int64{unix.EXT4_SUPER_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, unix.TMPFS_MAGIC} {
		if got := unsupportedFilesystem(&unix.Statfs_t{Type: kind}); got != "" {
			t.Fatalf("rejected local qualification filesystem %#x: %s", kind, got)
		}
	}
}
