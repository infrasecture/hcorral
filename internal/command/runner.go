package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
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
	cmd.WaitDelay = time.Second
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return Result{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, commandError(ctx, err)
}

func (ExecRunner) Run(ctx context.Context, argv, env []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(argv) == 0 || argv[0] == "" {
		return fmt.Errorf("empty command")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.WaitDelay = time.Second
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = env, stdin, stdout, stderr
	return commandError(ctx, cmd.Run())
}

// CommandContext can report an ExitError ("signal: killed") when cancellation
// terminates a running child. Preserve the context cause as well as that exit
// status, so callers can distinguish cancellation from an ordinary failure.
// A command that already succeeded stays successful even if cancellation races
// with its return; confirmed work must not become an invented failure.
func commandError(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	return err
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
