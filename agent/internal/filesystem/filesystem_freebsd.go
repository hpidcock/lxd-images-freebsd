//go:build freebsd

package filesystem

import (
	"path/filepath"

	"golang.org/x/sys/unix"
)

// Mounts returns the currently mounted filesystems.
func Mounts() ([]Mount, error) {
	n, err := unix.Getfsstat(nil, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}

	buf := make([]unix.Statfs_t, n+8)
	n, err = unix.Getfsstat(buf, unix.MNT_NOWAIT)
	if err != nil {
		return nil, err
	}

	mounts := make([]Mount, 0, n)
	for _, st := range buf[:n] {
		mounts = append(mounts, Mount{
			Source:     unix.ByteSliceToString(st.Mntfromname[:]),
			Mountpoint: unix.ByteSliceToString(st.Mntonname[:]),
			FSType:     unix.ByteSliceToString(st.Fstypename[:]),
			Stat:       st,
		})
	}

	return mounts, nil
}

// IsMountPoint returns true if path is a mount point.
func IsMountPoint(path string) bool {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return false
	}

	real, err = filepath.Abs(real)
	if err != nil {
		return false
	}

	st, err := StatVFS(real)
	if err != nil {
		return false
	}

	return unix.ByteSliceToString(st.Mntonname[:]) == real
}
