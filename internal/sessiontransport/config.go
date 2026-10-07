package sessiontransport

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/infrasecture/hcorral/internal/identity"
)

const maximumConfigBytes = 8 << 20

// ReadConfig reads a selected configuration file from the actual workstation,
// including its writable layer, while running or stopped. It does not start a
// helper, extract an archive onto the host, or run the image's entrypoint.
// Callers choose only relevant Codex configuration paths, never credentials.
func (d Docker) ReadConfig(ctx context.Context, workspace identity.Workspace, target Target, path string) ([]byte, error) {
	if !absolutePath(path) || path == "/" {
		return nil, errors.New("container configuration path must be an absolute file path")
	}
	if err := d.recheck(ctx, workspace, target); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	archive := &boundedBuffer{maximum: maximumConfigBytes + (64 << 10), description: "configuration archive"}
	stderr := &boundedBuffer{maximum: 16 << 10, description: "Docker configuration-read error"}
	err := d.Runtime.Runner.Run(ctx, []string{"docker", "cp", "-L", target.ContainerID + ":" + path, "-"}, d.Runtime.Env, nil, archive, stderr)
	if err != nil {
		// An unavailable container or failed transport is not a missing config.
		// Docker's missing-file diagnostic is checked narrowly, then ownership
		// and storage are rechecked before the caller may use an absent layer.
		missing := "Could not find the file " + path + " in container " + target.ContainerID
		if strings.Contains(stderr.String(), missing) {
			if checkErr := d.recheck(ctx, workspace, target); checkErr != nil {
				return nil, checkErr
			}
			return nil, &os.PathError{Op: "read container config", Path: path, Err: os.ErrNotExist}
		}
		return nil, fmt.Errorf("read container configuration %s: %w", path, err)
	}
	r := tar.NewReader(bytes.NewReader(archive.Bytes()))
	header, err := r.Next()
	if err != nil {
		return nil, fmt.Errorf("read configuration archive: %w", err)
	}
	if (header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA) || header.Size < 0 || header.Size > maximumConfigBytes {
		return nil, errors.New("configuration archive must contain one bounded regular file")
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read archived configuration: %w", err)
	}
	if _, err := r.Next(); err != io.EOF {
		return nil, errors.New("configuration archive contains extra or invalid members")
	}
	if err := d.recheck(ctx, workspace, target); err != nil {
		return nil, err
	}
	return data, nil
}
