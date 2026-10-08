// Package filesystem is a small, portable replacement for the parts of
// github.com/canonical/lxd/lxd/storage/filesystem used by the agent.
package filesystem

import (
	"golang.org/x/sys/unix"
)

// Mount describes a mounted filesystem.
type Mount struct {
	Source     string
	Mountpoint string
	FSType     string
	Stat       unix.Statfs_t
}

// StatVFS retrieves Virtual File System (VFS) info about a path.
func StatVFS(path string) (*unix.Statfs_t, error) {
	var st unix.Statfs_t

	err := unix.Statfs(path, &st)
	if err != nil {
		return nil, err
	}

	return &st, nil
}
