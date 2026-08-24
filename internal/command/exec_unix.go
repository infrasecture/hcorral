//go:build unix

package command

import "syscall"

func syscallExec(path string, argv, env []string) error {
	return syscall.Exec(path, argv, env)
}
