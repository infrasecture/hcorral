package session

import (
	"strings"

	"golang.org/x/sys/unix"
)

func unsupportedFilesystem(fs *unix.Statfs_t) string {
	kind := unix.ByteSliceToString(fs.Fstypename[:])
	if fs.Flags&unix.MNT_LOCAL == 0 || strings.Contains(strings.ToLower(kind), "fuse") || kind == "virtiofs" || kind == "9p" {
		if kind == "" {
			return "unknown non-local filesystem"
		}
		return kind
	}
	return ""
}
