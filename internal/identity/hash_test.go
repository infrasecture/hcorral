package identity

import (
	"crypto/sha256"
	"encoding/hex"
)

func newWorkspaceHash(path string) string {
	h := sha256.New()
	_, _ = h.Write([]byte(workspaceNamespace))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(path))
	return hex.EncodeToString(h.Sum(nil))
}
