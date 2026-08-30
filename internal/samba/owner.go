package samba

import (
	"os"
	"syscall"
)

// idsOf pulls the owning uid and gid out of a stat result.
//
// It is in a file of its own because the numbers live in a structure Go does
// not expose through os.FileInfo: keeping the type assertion here means the
// rest of the backend is plain Go, and there is one place to look when a
// platform answers differently.
func idsOf(info os.FileInfo) (uid, gid uint32, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return stat.Uid, stat.Gid, true
}
