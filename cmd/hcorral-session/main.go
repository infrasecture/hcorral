// hcorral-session is supplied by the launcher, not installed in workstation
// images. It runs as the endpoint's intended user without a shell or a TTY.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/infrasecture/hcorral/internal/sessionhelper"
	"golang.org/x/sys/unix"
)

func main() {
	os.Exit(run())
}

func run() int {
	unix.Umask(0o077)
	input, err := cancellablePipe(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hcorral-session: prepare stdin: %v\n", err)
		return 1
	}
	defer input.Close()
	output, err := cancellablePipe(os.Stdout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hcorral-session: prepare stdout: %v\n", err)
		return 1
	}
	defer output.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// A pipe may otherwise remain blocked in Read/Write after a signal. Closing
	// the descriptors releases it so Run can remove its own staging and locks.
	stopClosing := context.AfterFunc(ctx, func() {
		input.Close()
		output.Close()
	})
	defer stopClosing()
	if err := sessionhelper.Run(ctx, os.Args[1:], input, output); err != nil {
		fmt.Fprintf(os.Stderr, "hcorral-session: %v\n", err)
		return 1
	}
	return 0
}

// Inherited stdio pipes can be blocking descriptors that Go did not register
// with its poller. Close alone cannot interrupt a syscall already reading one.
// Reopen a nonblocking duplicate so Go can cancel pending pipe operations.
// Do not alter terminal flags when the helper is used directly for diagnostics.
func cancellablePipe(file *os.File) (*os.File, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Mode()&(os.ModeNamedPipe|os.ModeSocket) == 0 {
		return file, nil
	}
	fd, err := unix.Dup(int(file.Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		unix.Close(fd)
		return nil, err
	}
	return os.NewFile(uintptr(fd), file.Name()), nil
}
