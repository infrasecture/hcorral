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
	"github.com/infrasecture/hcorral/internal/sessionconfig"
)

const maximumConfigBytes = 8 << 20

// ResolveSQLiteHome brackets the whole configuration discovery with ownership
// and storage checks. Rechecking around every candidate file adds four Docker
// processes per file without giving the discovery an atomic filesystem view.
// Read from the actual workstation, including its writable layer, even while
// stopped; a helper created from its image would miss that configuration.
func (d Docker) ResolveSQLiteHome(ctx context.Context, workspace identity.Workspace, target Target, explicit string) (sessionconfig.Resolution, error) {
	options := sessionconfig.SQLiteOptions{Home: target.CodexHome, CWD: target.Workdir, Environment: target.SQLiteEnv, Explicit: explicit}
	if explicit != "" {
		return sessionconfig.SQLiteHome(ctx, options, nil)
	}
	if err := d.recheck(ctx, workspace, target); err != nil {
		return sessionconfig.Resolution{}, err
	}
	result, err := sessionconfig.SQLiteHome(ctx, options, func(ctx context.Context, name string) ([]byte, error) {
		return d.readConfig(ctx, target.ContainerID, name)
	})
	if err != nil {
		return sessionconfig.Resolution{}, err
	}
	if err := d.recheck(ctx, workspace, target); err != nil {
		return sessionconfig.Resolution{}, err
	}
	return result, nil
}

// readConfig only reads bounded configuration files; it never starts a process
// in the workstation or extracts its filesystem onto the host. The caller owns
// the checks around the complete discovery operation.
func (d Docker) readConfig(ctx context.Context, containerID, path string) ([]byte, error) {
	if !absolutePath(path) || path == "/" {
		return nil, errors.New("container configuration path must be an absolute file path")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	archive := &boundedBuffer{maximum: maximumConfigBytes + (64 << 10), description: "configuration archive"}
	stderr := &boundedBuffer{maximum: 16 << 10, description: "Docker configuration-read error"}
	err := d.Runtime.Runner.Run(ctx, []string{"docker", "cp", "-L", containerID + ":" + path, "-"}, d.Runtime.Env, nil, archive, stderr)
	if err != nil {
		// An unavailable container or failed transport is not a missing config.
		// Docker's missing-file diagnostic is checked narrowly. The caller
		// rechecks ownership and storage before using the resolved location.
		missing := "Could not find the file " + path + " in container " + containerID
		if strings.Contains(stderr.String(), missing) {
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
	return data, nil
}
