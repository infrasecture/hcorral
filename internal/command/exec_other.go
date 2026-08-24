//go:build !unix

package command

import "fmt"

func syscallExec(string, []string, []string) error {
	return fmt.Errorf("process replacement unsupported on this platform")
}
