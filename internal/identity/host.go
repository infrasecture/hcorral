package identity

import (
	"fmt"
	"os"
	"os/user"
	"sort"
	"strconv"
)

// HostIdentity carries numeric process credentials. Account names are only
// labels: static Linux builds cannot rely on libc/NSS for account discovery.
type HostIdentity struct {
	UID, GID, User, Group string
	Groups                []string
}

func CurrentHost() (HostIdentity, error) {
	groups, err := os.Getgroups()
	if err != nil {
		return HostIdentity{}, fmt.Errorf("read process supplementary groups: %w", err)
	}
	return hostIdentity(os.Geteuid(), os.Getegid(), groups), nil
}

func hostIdentity(uid, gid int, supplementary []int) HostIdentity {
	identity := HostIdentity{UID: strconv.Itoa(uid), GID: strconv.Itoa(gid)}
	identity.User = "hcorral-" + identity.UID
	if account, err := user.LookupId(identity.UID); err == nil && account.Username != "" {
		identity.User = account.Username
	}
	identity.Group = groupName(identity.GID)
	unique := map[int]bool{gid: true}
	for _, group := range supplementary {
		unique[group] = true
	}
	groups := make([]int, 0, len(unique))
	for group := range unique {
		groups = append(groups, group)
	}
	sort.Ints(groups)
	for _, group := range groups {
		id := strconv.Itoa(group)
		identity.Groups = append(identity.Groups, id+":"+groupName(id))
	}
	return identity
}

func groupName(id string) string {
	if group, err := user.LookupGroupId(id); err == nil && group.Name != "" {
		return group.Name
	}
	return "group-" + id
}
