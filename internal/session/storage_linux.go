package session

import "golang.org/x/sys/unix"

// A successful local flock does not prove remote/host lock visibility. FUSE
// includes virtiofs and SSHFS; the Colima host/guest regression demonstrates
// this distinction. Network filesystem behavior also varies with mount/server
// options. This guard rejects known unqualified classes, not a proof that an
// arbitrary layered filesystem or nonparticipating writer is safe.
func unsupportedFilesystem(fs *unix.Statfs_t) string {
	switch fs.Type {
	case unix.FUSE_SUPER_MAGIC:
		return "FUSE (including virtiofs/SSHFS)"
	case unix.V9FS_MAGIC:
		return "9p"
	case unix.NFS_SUPER_MAGIC:
		return "NFS"
	case unix.CIFS_SUPER_MAGIC, unix.SMB_SUPER_MAGIC, unix.SMB2_SUPER_MAGIC:
		return "SMB/CIFS"
	}
	return ""
}
