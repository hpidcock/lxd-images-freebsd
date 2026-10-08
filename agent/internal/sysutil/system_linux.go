//go:build linux

package sysutil

import (
	"context"
	"os"

	"github.com/canonical/lxd/shared"
	"golang.org/x/sys/unix"
)

// DevLXDSocketDir is the directory under which the <dir>/lxd/sock devlxd socket is created.
const DevLXDSocketDir = "/dev"

// AgentStatusDevice is the virtio-serial port LXD reads the agent status from.
const AgentStatusDevice = "/dev/virtio-ports/com.canonical.lxd"

// LoadModule loads the given kernel module.
func LoadModule(module string) error {
	_, err := shared.RunCommand(context.TODO(), "modprobe", module)
	return err
}

// MountArgs returns arguments for mount(8).
func MountArgs(fsType string, source string, target string, options []string) ([]string, error) {
	args := []string{"-t", fsType}
	for _, opt := range options {
		args = append(args, "-o", opt)
	}

	return append(args, source, target), nil
}

// Unmount unmounts the given mount point.
func Unmount(target string) error {
	return unix.Unmount(target, unix.MNT_DETACH)
}

// PrepareStatusDevice is a no-op on Linux.
func PrepareStatusDevice(f *os.File) error {
	return nil
}
