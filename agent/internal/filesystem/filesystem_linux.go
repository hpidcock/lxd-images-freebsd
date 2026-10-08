//go:build linux

package filesystem

import (
	"bufio"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Mounts returns the currently mounted filesystems.
func Mounts() ([]Mount, error) {
	f, err := os.Open("/proc/mounts")
	if err != nil {
		return nil, err
	}

	defer func() { _ = f.Close() }()

	var mounts []Mount
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 3 {
			continue
		}

		m := Mount{Source: fields[0], Mountpoint: fields[1], FSType: fields[2]}
		_ = unix.Statfs(m.Mountpoint, &m.Stat)
		mounts = append(mounts, m)
	}

	return mounts, scanner.Err()
}

// IsMountPoint returns true if path is a mount point.
func IsMountPoint(path string) bool {
	stat, err := os.Stat(path)
	if err != nil {
		return false
	}

	rootStat, err := os.Lstat(path + "/..")
	if err != nil {
		return false
	}

	return stat.Sys().(*syscall.Stat_t).Dev != rootStat.Sys().(*syscall.Stat_t).Dev
}
