package identity

import (
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestHostIdentityRetainsUnknownNumericCredentials(t *testing.T) {
	t.Parallel()
	groups := []int{2147483002, 2147483001, 2147483002}
	got := hostIdentity(2147483000, 2147483001, groups)
	if got.UID != "2147483000" || got.GID != "2147483001" || got.User != "hcorral-2147483000" || got.Group != "group-2147483001" {
		t.Fatalf("numeric fallback = %#v", got)
	}
	want := []string{"2147483001:group-2147483001", "2147483002:group-2147483002"}
	if !reflect.DeepEqual(got.Groups, want) {
		t.Fatalf("supplementary groups = %v, want %v", got.Groups, want)
	}
	if groups[0] != 2147483002 {
		t.Fatal("modified caller's group list")
	}
}

func TestCurrentHostUsesProcessGroups(t *testing.T) {
	t.Parallel()
	got, err := CurrentHost()
	if err != nil {
		t.Fatal(err)
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{strconv.Itoa(os.Getegid()): true}
	for _, gid := range groups {
		want[strconv.Itoa(gid)] = true
	}
	actual := map[string]bool{}
	for _, spec := range got.Groups {
		id, _, _ := strings.Cut(spec, ":")
		actual[id] = true
	}
	if got.UID != strconv.Itoa(os.Geteuid()) || got.GID != strconv.Itoa(os.Getegid()) || !reflect.DeepEqual(actual, want) {
		t.Fatalf("process identity = %#v; want groups %v", got, want)
	}
}
