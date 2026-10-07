package session

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

var ErrUnsupportedStorage = errors.New("unsupported session storage filesystem")

// storageFile takes ownership of fd, including on failure. Check the opened
// object, not its pathname: a bind mount can replace a path or a descendant can
// live on a different filesystem from the selected home.
func storageFile(fd int, name string) (*os.File, error) {
	var fs unix.Statfs_t
	if err := unix.Fstatfs(fd, &fs); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("inspect session storage %q: %w", name, err)
	}
	if kind := unsupportedFilesystem(&fs); kind != "" {
		unix.Close(fd)
		return nil, fmt.Errorf("%w %q at %q: writer locks cannot be assumed to coordinate across its clients; use native local storage or a daemon-local volume", ErrUnsupportedStorage, kind, name)
	}
	return os.NewFile(uintptr(fd), name), nil
}
