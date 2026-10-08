//go:build linux

package sysutil

import (
	"os"

	"github.com/canonical/lxd/shared"
)

// OpenPty creates a new PTS pair, configures them and returns them.
func OpenPty(uid, gid int64) (ptx *os.File, pty *os.File, err error) {
	return shared.OpenPty(uid, gid)
}

// SetSize sets the terminal size to the specified width and height for the given file descriptor.
func SetSize(fd int, width int, height int) error {
	return shared.SetSize(fd, width, height)
}
