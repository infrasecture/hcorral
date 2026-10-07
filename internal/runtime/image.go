package runtime

import "strings"

// IsLatest includes an omitted tag, which Docker resolves as :latest. Digests
// and all explicitly named tags other than latest remain deliberate pins.
func IsLatest(reference string) bool {
	if reference == "" || strings.ContainsRune(reference, '@') {
		return false
	}
	name := reference[strings.LastIndexByte(reference, '/')+1:]
	colon := strings.LastIndexByte(name, ':')
	return colon < 0 || name[colon+1:] == "latest"
}
