package update

import (
	"fmt"
	"strconv"
	"strings"
)

type Version struct {
	Major, Minor, Patch int
	Pre                 []string
	Raw                 string
}

func Parse(value string) (Version, error) {
	raw := strings.TrimPrefix(strings.TrimSpace(value), "v")
	withoutBuild := strings.SplitN(raw, "+", 2)[0]
	parts := strings.SplitN(withoutBuild, "-", 2)
	core := strings.Split(parts[0], ".")
	if len(core) != 3 {
		return Version{}, fmt.Errorf("version %q is not SemVer", value)
	}
	numbers := [3]int{}
	for index, item := range core {
		if item == "" || (len(item) > 1 && item[0] == '0') {
			return Version{}, fmt.Errorf("version %q has a non-canonical numeric component", value)
		}
		parsed, err := strconv.Atoi(item)
		if err != nil || parsed < 0 {
			return Version{}, fmt.Errorf("version %q has an invalid numeric component", value)
		}
		numbers[index] = parsed
	}
	version := Version{Major: numbers[0], Minor: numbers[1], Patch: numbers[2], Raw: raw}
	if len(parts) == 2 {
		if parts[1] == "" {
			return Version{}, fmt.Errorf("version %q has empty prerelease", value)
		}
		version.Pre = strings.Split(parts[1], ".")
		for _, identifier := range version.Pre {
			if identifier == "" {
				return Version{}, fmt.Errorf("version %q has empty prerelease identifier", value)
			}
		}
	}
	return version, nil
}

func Compare(left, right Version) int {
	for _, pair := range [][2]int{{left.Major, right.Major}, {left.Minor, right.Minor}, {left.Patch, right.Patch}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	if len(left.Pre) == 0 && len(right.Pre) > 0 {
		return 1
	}
	if len(left.Pre) > 0 && len(right.Pre) == 0 {
		return -1
	}
	for index := 0; index < len(left.Pre) && index < len(right.Pre); index++ {
		a, b := left.Pre[index], right.Pre[index]
		if a == b {
			continue
		}
		ai, ae := strconv.Atoi(a)
		bi, be := strconv.Atoi(b)
		switch {
		case ae == nil && be == nil:
			if ai < bi {
				return -1
			}
			return 1
		case ae == nil:
			return -1
		case be == nil:
			return 1
		default:
			if a < b {
				return -1
			}
			return 1
		}
	}
	if len(left.Pre) < len(right.Pre) {
		return -1
	}
	if len(left.Pre) > len(right.Pre) {
		return 1
	}
	return 0
}
