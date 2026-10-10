//go:build linux

package process

import (
	"reflect"
	"strings"
	"testing"
)

func TestGroupMembersReportsLiveMembersAndSkipsZombies(t *testing.T) {
	root := t.TempDir()
	writeFakeProcStat(t, root, 42, procStatLine(42, "leader", 42, 10))
	writeFakeProcStat(t, root, 43, procStatLine(43, "same-group", 42, 20))
	writeFakeProcStat(t, root, 44, procStatLineState(44, "zombie", 'Z', 42, 30))
	writeFakeProcStat(t, root, 45, procStatLine(45, "other-group", 45, 40))

	members, err := groupMembers(root, 42)
	if err != nil {
		t.Fatalf("groupMembers: %v", err)
	}
	if want := []int{42, 43}; !reflect.DeepEqual(members, want) {
		t.Fatalf("group members = %v, want %v", members, want)
	}
}

func TestGroupMembersDoesNotTreatMalformedProcEntriesAsEmptyGroup(t *testing.T) {
	root := t.TempDir()
	writeFakeProcStat(t, root, 42, []byte("malformed"))
	members, err := groupMembers(root, 42)
	if err == nil {
		t.Fatalf("incomplete procfs scan returned members %v with no error", members)
	}
}

func procStatLineState(pid int, command string, state byte, pgid int, startTicks uint64) []byte {
	line := procStatLine(pid, command, pgid, startTicks)
	return []byte(strings.Replace(string(line), ") S ", ") "+string(state)+" ", 1))
}
