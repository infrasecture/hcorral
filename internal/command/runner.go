package command

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
)

type Result struct {
	Stdout []byte
	Stderr []byte
}

type Runner interface {
	Capture(context.Context, []string, []string) (Result, error)
	Run(context.Context, []string, []string, io.Reader, io.Writer, io.Writer) error
	Replace([]string, []string) error
}

type ExecRunner struct{}

func (ExecRunner) Capture(ctx context.Context, argv, env []string) (Result, error) {
	if len(argv) == 0 || argv[0] == "" {
		return Result{}, fmt.Errorf("empty command")
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}

func (ExecRunner) Run(ctx context.Context, argv, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(argv) == 0 || argv[0] == "" {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, stdin, stdout, stderr
	return cmd.Run()
}

func (ExecRunner) Replace(argv, env []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return fmt.Errorf("empty command")
	}
	path, err := exec.LookPath(argv[0])
	if err != nil {
		return err
	}
	return syscallExec(path, argv, env)
}

func EnvironmentWithoutCompose(environ []string) []string {
	filtered := make([]string, 0, len(environ))
	for _, item := range environ {
		if len(item) >= len("COMPOSE_") && item[:len("COMPOSE_")] == "COMPOSE_" {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func CurrentEnvironment() []string { return os.Environ() }
