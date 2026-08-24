//go:build !linux && !darwin

package identity

import "fmt"

type Lock struct{}

func AcquireLock(string) (*Lock, error) {
	return nil, fmt.Errorf("mutation locks unsupported on this platform")
}
func (*Lock) Close() error { return nil }
