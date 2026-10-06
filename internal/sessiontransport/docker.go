package sessiontransport

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/infrasecture/hcorral/internal/identity"
	containerruntime "github.com/infrasecture/hcorral/internal/runtime"
)

const helperLabel = "ai.infrasecture.hcorral.transfer"
const helperPath = "/hcorral-session"

type Docker struct {
	Runtime containerruntime.Docker
	// Helper returns the launcher-supplied Linux executable for the inspected
	// image architecture. Host architecture is never used to choose it.
	Helper func(architecture string) ([]byte, error)
}

// Run creates a disposable helper, even when the workstation is running. That
// gives cancellation an independently stoppable process and never injects code
// into a user's tmux session or changes the workstation's writable rootfs.
func (d Docker) Run(ctx context.Context, workspace identity.Workspace, target Target, args []string, input io.Reader, output, stderr io.Writer) (resultErr error) {
	if err := d.recheck(ctx, workspace, target); err != nil {
		return err
	}
	for _, mount := range target.Mounts {
		if contains(mount.Destination, helperPath) || contains(helperPath, mount.Destination) {
			return errors.New("selected storage mount would overlap the supplied helper executable")
		}
	}
	image, err := d.Runtime.InspectImage(ctx, target.ImageID)
	if err != nil {
		return err
	}
	if image == nil || image.ID != target.ImageID {
		return errors.New("the workstation's actual image is unavailable locally; no image was pulled")
	}
	if image.OS != "linux" || (image.Architecture != "amd64" && image.Architecture != "arm64") {
		return fmt.Errorf("unsupported helper image platform %s/%s", image.OS, image.Architecture)
	}
	var imageVolumes []string
	for volume := range image.Config.Volumes {
		if !absolutePath(volume) || contains(volume, helperPath) || contains(helperPath, volume) {
			return fmt.Errorf("image volume %q would hide the supplied helper", volume)
		}
		imageVolumes = append(imageVolumes, volume)
	}
	sort.Strings(imageVolumes)
	if d.Helper == nil {
		return errors.New("launcher has no bundled session helper")
	}
	helper, err := d.Helper(image.Architecture)
	if err != nil {
		return err
	}
	if len(helper) == 0 {
		return errors.New("bundled session helper is empty")
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	token := hex.EncodeToString(nonce[:])
	name := "hcorral-transfer-" + token
	// Also handles an uncertain create response: locate only our random name
	// and exact ownership token, never remove by a broad name prefix.
	defer func() { resultErr = errors.Join(resultErr, d.cleanup(name, token)) }()
	argv := []string{"docker", "create", "--name", name, "--label", helperLabel + "=" + token,
		"--network", "none", "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
		"--pids-limit", "64", "--restart", "no", "--stop-signal", "SIGTERM", "--pull", "never", "--no-healthcheck", "--interactive",
		"--user", target.UID + ":" + target.GID, "--workdir", "/", "--entrypoint", helperPath,
		"--env", "HOME=" + target.Home}
	for _, gid := range target.Groups {
		argv = append(argv, "--group-add", gid)
	}
	for _, mount := range target.Mounts {
		argv = append(argv, "--mount", mountArgument(mount))
	}
	// An image VOLUME instruction must not create anonymous persistent state
	// as a side effect. Cover unused declarations with small ephemeral tmpfs.
	for _, volume := range imageVolumes {
		covered := false
		for _, mount := range target.Mounts {
			if mount.Destination == volume {
				covered = true
				break
			}
		}
		if !covered {
			if contains(target.CodexHome, volume) || contains(target.SQLiteHome, volume) {
				return fmt.Errorf("image volume %s would shadow selected Codex storage", volume)
			}
			argv = append(argv, "--mount", csvFields([]string{"type=tmpfs", "dst=" + volume, "tmpfs-size=1048576", "tmpfs-mode=0755"}))
		}
	}
	argv = append(argv, target.ImageID)
	argv = append(argv, args...)
	createCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	created, err := d.Runtime.Runner.Capture(createCtx, argv, d.Runtime.Env)
	cancel()
	if err != nil {
		return fmt.Errorf("create transfer helper: %w: %s", err, strings.TrimSpace(string(created.Stderr)))
	}
	id := strings.TrimSpace(string(created.Stdout))
	if len(id) != 64 {
		return errors.New("Docker returned an invalid helper container ID")
	}
	if _, err := hex.DecodeString(id); err != nil {
		return errors.New("Docker returned a non-hexadecimal helper container ID")
	}
	owned, err := d.Runtime.InspectContainer(ctx, id)
	if err != nil || owned == nil || owned.Config.Labels[helperLabel] != token || owned.CleanName() != name {
		return fmt.Errorf("cannot verify the created transfer helper: %w", errors.Join(err, errors.New("helper ownership mismatch")))
	}
	// Supply executable bytes through Docker, never a client-host bind mount.
	var archive bytes.Buffer
	w := tar.NewWriter(&archive)
	if err := w.WriteHeader(&tar.Header{Name: strings.TrimPrefix(helperPath, "/"), Typeflag: tar.TypeReg, Mode: 0o555, Size: int64(len(helper))}); err != nil {
		return err
	}
	if _, err := w.Write(helper); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	copyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = d.Runtime.Runner.Run(copyCtx, []string{"docker", "cp", "-", id + ":/"}, d.Runtime.Env, &archive, io.Discard, stderr)
	cancel()
	if err != nil {
		return fmt.Errorf("supply transfer helper: %w", err)
	}
	if err := d.recheck(ctx, workspace, target); err != nil {
		return err
	}
	if err := d.Runtime.Runner.Run(ctx, []string{"docker", "start", "--attach", "--interactive", id}, d.Runtime.Env, input, output, stderr); err != nil {
		return fmt.Errorf("session helper: %w", err)
	}
	finished, err := d.Runtime.InspectContainer(ctx, id)
	if err != nil {
		return err
	}
	if finished == nil || finished.Config.Labels[helperLabel] != token || finished.State.Status != "exited" || finished.State.ExitCode != 0 {
		return errors.New("session helper did not finish successfully; an import may already have published its files")
	}
	return nil
}

func mountArgument(mount containerruntime.Mount) string {
	fields := []string{"type=" + mount.Type, "dst=" + mount.Destination}
	if mount.Type == "volume" {
		fields = append(fields, "src="+mount.Name, "volume-nocopy=true")
		if mount.Subpath != "" {
			fields = append(fields, "volume-subpath="+mount.Subpath)
		}
	} else {
		fields = append(fields, "src="+mount.Source, "bind-propagation=rprivate")
	}
	if !mount.RW {
		fields = append(fields, "readonly=true")
	}
	return csvFields(fields)
}

func csvFields(fields []string) string {
	var out strings.Builder
	w := csv.NewWriter(&out)
	_ = w.Write(fields)
	w.Flush()
	return strings.TrimSuffix(out.String(), "\n")
}

func (d Docker) recheck(ctx context.Context, workspace identity.Workspace, target Target) error {
	container, err := d.Runtime.InspectContainer(ctx, target.ContainerID)
	if err != nil {
		return err
	}
	if err := identity.VerifyContainer(container, workspace); err != nil {
		return err
	}
	current, err := InspectTargetWithSQLite(container, target.SQLiteHome)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, target) {
		return errors.New("workstation identity, image, runtime settings or mounts changed during transfer preparation")
	}
	for _, mount := range target.Mounts {
		if mount.Type != "volume" {
			continue
		}
		volume, err := d.Runtime.InspectVolume(ctx, mount.Name)
		if err != nil {
			return err
		}
		if volume == nil {
			return fmt.Errorf("selected state volume %s is missing; no volume was created", mount.Name)
		}
		if owner := volume.Labels[identity.LabelWorkspaceID]; owner != "" && owner != workspace.FullID {
			return fmt.Errorf("state volume %s belongs to a different workspace", mount.Name)
		}
		if schema := volume.Labels[identity.LabelRuntimeSchema]; schema != "" && schema != identity.RuntimeSchemaVersion {
			return fmt.Errorf("state volume %s has an unsupported runtime schema", mount.Name)
		}
	}
	return nil
}

func (d Docker) cleanup(name, token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	container, err := d.Runtime.InspectContainer(ctx, name)
	if err != nil || container == nil {
		return err
	}
	if container.CleanName() != name || container.Config.Labels[helperLabel] != token {
		return fmt.Errorf("preserved helper name %s because its ownership token changed", name)
	}
	if container.State.Running {
		// Give the helper's signal handler a bounded opportunity to clean up
		// staging. Docker terminates it after the timeout if it cannot respond.
		if err := d.Runtime.Runner.Run(ctx, []string{"docker", "stop", "--time", "2", container.ID}, d.Runtime.Env, nil, io.Discard, d.Runtime.Err); err != nil {
			return fmt.Errorf("stop transfer helper %s: %w", name, err)
		}
	}
	if err := d.Runtime.Runner.Run(ctx, []string{"docker", "rm", container.ID}, d.Runtime.Env, nil, io.Discard, d.Runtime.Err); err != nil {
		return fmt.Errorf("remove transfer helper %s: %w", name, err)
	}
	return nil
}
