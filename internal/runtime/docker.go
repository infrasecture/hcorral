package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/infrasecture/hcorral/internal/command"
)

var ErrNotFound = errors.New("Docker object not found")

type Mount struct {
	Type        string `json:"Type"`
	Name        string `json:"Name"`
	Source      string `json:"Source"`
	Destination string `json:"Destination"`
	RW          bool   `json:"RW"`
}

type Container struct {
	ID      string `json:"Id"`
	Name    string `json:"Name"`
	Created string `json:"Created"`
	Config  struct {
		Image  string            `json:"Image"`
		Labels map[string]string `json:"Labels"`
		Env    []string          `json:"Env"`
	} `json:"Config"`
	State struct {
		Status  string `json:"Status"`
		Running bool   `json:"Running"`
		Started string `json:"StartedAt"`
	} `json:"State"`
	Mounts []Mount `json:"Mounts"`
}

func (c Container) CleanName() string { return strings.TrimPrefix(c.Name, "/") }

type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
}

type Network struct {
	ID     string            `json:"Id"`
	Name   string            `json:"Name"`
	Driver string            `json:"Driver"`
	Labels map[string]string `json:"Labels"`
}

type Image struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

type Docker struct {
	Runner command.Runner
	Env    []string
	Out    io.Writer
	Err    io.Writer
}

func NewDocker(runner command.Runner) Docker {
	return Docker{Runner: runner, Env: command.EnvironmentWithoutCompose(os.Environ()), Out: os.Stdout, Err: os.Stderr}
}

func (d Docker) WithStreams(out, stderr io.Writer) Docker { d.Out, d.Err = out, stderr; return d }

func (d Docker) ListContainers(ctx context.Context) ([]Container, error) {
	result, err := d.capture(ctx, []string{"docker", "ps", "-aq", "--no-trunc"})
	if err != nil {
		return nil, err
	}
	ids := strings.Fields(string(result.Stdout))
	if len(ids) == 0 {
		return []Container{}, nil
	}
	return d.InspectContainers(ctx, ids...)
}

func (d Docker) InspectContainers(ctx context.Context, names ...string) ([]Container, error) {
	if len(names) == 0 {
		return []Container{}, nil
	}
	argv := append([]string{"docker", "inspect", "--type", "container"}, names...)
	result, err := d.capture(ctx, argv)
	if err != nil {
		if isNotFound(result.Stderr) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	var containers []Container
	if err := json.Unmarshal(result.Stdout, &containers); err != nil {
		return nil, fmt.Errorf("decode Docker container inspection: %w", err)
	}
	return containers, nil
}

func (d Docker) InspectContainer(ctx context.Context, name string) (*Container, error) {
	containers, err := d.InspectContainers(ctx, name)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(containers) != 1 {
		return nil, fmt.Errorf("Docker returned %d containers for %q", len(containers), name)
	}
	return &containers[0], nil
}

func (d Docker) InspectVolume(ctx context.Context, name string) (*Volume, error) {
	result, err := d.capture(ctx, []string{"docker", "volume", "inspect", name})
	if err != nil {
		if isNotFound(result.Stderr) {
			return nil, nil
		}
		return nil, err
	}
	var volumes []Volume
	if err := json.Unmarshal(result.Stdout, &volumes); err != nil {
		return nil, fmt.Errorf("decode Docker volume inspection: %w", err)
	}
	if len(volumes) != 1 {
		return nil, fmt.Errorf("Docker returned %d volumes for %q", len(volumes), name)
	}
	return &volumes[0], nil
}

func (d Docker) InspectNetwork(ctx context.Context, name string) (*Network, error) {
	result, err := d.capture(ctx, []string{"docker", "network", "inspect", name})
	if err != nil {
		if isNotFound(result.Stderr) {
			return nil, nil
		}
		return nil, err
	}
	var networks []Network
	if err := json.Unmarshal(result.Stdout, &networks); err != nil {
		return nil, fmt.Errorf("decode Docker network inspection: %w", err)
	}
	if len(networks) != 1 {
		return nil, fmt.Errorf("Docker returned %d networks for %q", len(networks), name)
	}
	return &networks[0], nil
}

func (d Docker) CreateVolume(ctx context.Context, name string, labels map[string]string) error {
	argv := []string{"docker", "volume", "create"}
	for _, key := range sortedKeys(labels) {
		argv = append(argv, "--label", key+"="+labels[key])
	}
	argv = append(argv, name)
	return d.run(ctx, argv)
}

func (d Docker) RemoveVolume(ctx context.Context, name string) error {
	return d.run(ctx, []string{"docker", "volume", "rm", name})
}

func (d Docker) InspectImage(ctx context.Context, reference string) (*Image, error) {
	result, err := d.capture(ctx, []string{"docker", "image", "inspect", reference})
	if err != nil {
		if isNotFound(result.Stderr) {
			return nil, nil
		}
		return nil, err
	}
	var images []Image
	if err := json.Unmarshal(result.Stdout, &images); err != nil {
		return nil, fmt.Errorf("decode Docker image inspection: %w", err)
	}
	if len(images) != 1 {
		return nil, fmt.Errorf("Docker returned %d images for %q", len(images), reference)
	}
	return &images[0], nil
}

func (d Docker) PullImage(ctx context.Context, reference string, stdout, stderr io.Writer) error {
	return d.Runner.Run(ctx, []string{"docker", "pull", reference}, d.Env, nil, stdout, stderr)
}

func (d Docker) StartContainer(ctx context.Context, name string) error {
	return d.run(ctx, []string{"docker", "start", name})
}

func (d Docker) StopContainer(ctx context.Context, name string) error {
	return d.run(ctx, []string{"docker", "stop", name})
}

func (d Docker) ExecCapture(ctx context.Context, name string, args ...string) (command.Result, error) {
	argv := append([]string{"docker", "exec", name}, args...)
	return d.capture(ctx, argv)
}

func (d Docker) ContainerLogs(ctx context.Context, name string, tail int) (command.Result, error) {
	if tail <= 0 {
		tail = 80
	}
	return d.capture(ctx, []string{"docker", "logs", "--tail", fmt.Sprintf("%d", tail), name})
}

func (d Docker) capture(ctx context.Context, argv []string) (command.Result, error) {
	result, err := d.Runner.Capture(ctx, argv, d.Env)
	if err != nil {
		return result, fmt.Errorf("%s: %w: %s", strings.Join(argv[:min(3, len(argv))], " "), err, strings.TrimSpace(string(result.Stderr)))
	}
	return result, nil
}

func (d Docker) run(ctx context.Context, argv []string) error {
	if err := d.Runner.Run(ctx, argv, d.Env, nil, d.Out, d.Err); err != nil {
		return fmt.Errorf("%s: %w", strings.Join(argv[:min(3, len(argv))], " "), err)
	}
	return nil
}

func isNotFound(stderr []byte) bool {
	text := strings.ToLower(string(stderr))
	return strings.Contains(text, "no such object") ||
		strings.Contains(text, "no such container") ||
		strings.Contains(text, "no such volume") ||
		strings.Contains(text, "no such network") ||
		strings.Contains(text, "no such image") ||
		(strings.Contains(text, "network ") && strings.Contains(text, " not found"))
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
