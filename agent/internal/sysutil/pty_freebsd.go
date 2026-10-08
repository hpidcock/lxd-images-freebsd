//go:build freebsd

package sysutil

import (
	"fmt"
	"math"
	"os"
	"unsafe"

	"github.com/canonical/lxd/shared/revert"
	"golang.org/x/sys/unix"
)

// fiodgnameArg mirrors struct fiodgname_arg from sys/filio.h.
type fiodgnameArg struct {
	Len int32
	_   [4]byte
	Buf unsafe.Pointer
}

// ptsName returns the device name (relative to /dev) of the slave side of the
// pseudo-terminal whose master is fd, the way libc's ptsname(3) does it.
func ptsName(fd int) (string, error) {
	buf := make([]byte, 256) // SPECNAMELEN+1
	arg := fiodgnameArg{Len: int32(len(buf)), Buf: unsafe.Pointer(&buf[0])}

	// FIODGNAME is _IOW('f', 120, struct fiodgname_arg).
	req := uintptr(0x80000000) | uintptr(unsafe.Sizeof(arg))<<16 | uintptr('f')<<8 | 120

	_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), req, uintptr(unsafe.Pointer(&arg)))
	if errno != 0 {
		return "", fmt.Errorf("Failed getting pty name: %w", errno)
	}

	return unix.ByteSliceToString(buf), nil
}

// OpenPty creates a new PTS pair, configures them and returns them.
// The pty (slave) side is chowned to uid/gid.
func OpenPty(uid, gid int64) (ptx *os.File, pty *os.File, err error) {
	reverter := revert.New()
	defer reverter.Fail()

	// Open the master side.
	mfd, _, errno := unix.Syscall(unix.SYS_POSIX_OPENPT, uintptr(unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC), 0, 0)
	if errno != 0 {
		return nil, nil, fmt.Errorf("Failed opening pty master: %w", errno)
	}

	reverter.Add(func() { _ = unix.Close(int(mfd)) })

	name, err := ptsName(int(mfd))
	if err != nil {
		return nil, nil, err
	}

	// Open the slave side. grantpt()/unlockpt() are no-ops on FreeBSD.
	sfd, err := unix.Open("/dev/"+name, unix.O_RDWR|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, fmt.Errorf("Failed opening pty slave %q: %w", name, err)
	}

	reverter.Add(func() { _ = unix.Close(sfd) })

	// Configure both sides.
	for _, fd := range []int{int(mfd), sfd} {
		t, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
		if err != nil {
			return nil, nil, err
		}

		t.Iflag |= unix.IMAXBEL | unix.BRKINT | unix.IXANY
		t.Cflag |= unix.HUPCL

		err = unix.IoctlSetTermios(fd, unix.TIOCSETA, t)
		if err != nil {
			return nil, nil, err
		}

		// Set the default window size.
		err = unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: 80, Row: 25})
		if err != nil {
			return nil, nil, err
		}
	}

	// Fix the ownership of the pty side.
	err = unix.Fchown(sfd, int(uid), int(gid))
	if err != nil {
		return nil, nil, err
	}

	reverter.Success()
	return os.NewFile(mfd, "/dev/ptmx"), os.NewFile(uintptr(sfd), "/dev/"+name), nil
}

// SetSize sets the terminal size to the specified width and height for the given file descriptor.
func SetSize(fd int, width int, height int) error {
	if width > math.MaxUint16 || height > math.MaxUint16 {
		return fmt.Errorf("Width or height too large: %d %d", width, height)
	}

	if width < 0 || height < 0 {
		return fmt.Errorf("Width and height must not be negative: %d %d", width, height)
	}

	return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Col: uint16(width), Row: uint16(height)})
}
