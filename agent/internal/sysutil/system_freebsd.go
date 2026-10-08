//go:build freebsd

package sysutil

import (
	"context"
	"fmt"
	"os"
	"slices"

	"github.com/canonical/lxd/shared"
	"golang.org/x/sys/unix"
)

// DevLXDSocketDir is the directory under which the <dir>/lxd/sock devlxd
// socket is created. FreeBSD's /dev is devfs, where directories cannot be
// created, so the socket lives under /var/run instead of /dev.
const DevLXDSocketDir = "/var/run"

// AgentStatusDevice is the virtio-serial port LXD reads the agent status from.
const AgentStatusDevice = "/dev/vtcon/com.canonical.lxd"

// LoadModule loads the given kernel module (by file name, without .ko) if it is not already loaded.
func LoadModule(module string) error {
	// Check by file name: the module names registered inside the files
	// (kldstat -m) do not always match the file names.
	_, err := shared.RunCommand(context.TODO(), "kldstat", "-q", "-n", module+".ko")
	if err == nil {
		return nil
	}

	_, err = shared.RunCommand(context.TODO(), "kldload", module)
	if err != nil {
		return fmt.Errorf("Failed loading kernel module %q: %w", module, err)
	}

	return nil
}

// MountArgs translates an LXD VM agent mount (filesystem type and options as
// LXD expresses them for Linux guests) into arguments for mount(8).
func MountArgs(fsType string, source string, target string, options []string) ([]string, error) {
	var opts []string

	switch fsType {
	case "9p", "p9fs":
		// LXD's 9p shares are mounted with FreeBSD's p9fs over virtio. The
		// Linux 9p options (trans=virtio, msize, access, ...) do not apply.
		fsType = "p9fs"
		if slices.Contains(options, "ro") {
			opts = append(opts, "ro")
		}

	case "virtiofs":
		return nil, fmt.Errorf("virtiofs is not supported on FreeBSD")
	default:
		opts = options
	}

	args := []string{"-t", fsType}
	for _, opt := range opts {
		args = append(args, "-o", opt)
	}

	return append(args, source, target), nil
}

// Unmount unmounts the given mount point.
func Unmount(target string) error {
	return unix.Unmount(target, unix.MNT_FORCE)
}

// PrepareStatusDevice puts the agent status tty into raw output mode so that
// the status lines written to it are not subject to tty output processing.
func PrepareStatusDevice(f *os.File) error {
	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	if err != nil {
		return err
	}

	t.Oflag = 0
	return unix.IoctlSetTermios(fd, unix.TIOCSETA, t)
}
