//go:build freebsd

package vsock

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// afVsock is the AF_VSOCK address family number used by the out-of-tree FreeBSD
// vsock kernel module (sys/sys/socket.h in the virtio_vsocks branch). It is
// not part of FreeBSD's stock socket.h, so it is hardcoded here and in
// kmod/build.sh; the two must agree.
const afVsock = 48

// sockaddrVM mirrors struct sockaddr_vm from the vsock branch's
// sys/sys/vm_sockets.h, including the compiler padding after svm_family.
type sockaddrVM struct {
	Len    uint8
	Family uint8
	_      [2]byte
	Port   uint32
	CID    uint32
	Flags  uint8
	_      [7]byte
}

var sockaddrVMLen = uint32(unsafe.Sizeof(sockaddrVM{}))

func newSockaddr(cid uint32, port uint32) *sockaddrVM {
	return &sockaddrVM{
		Len:    uint8(sockaddrVMLen),
		Family: afVsock,
		Port:   port,
		CID:    cid,
	}
}

func (sa *sockaddrVM) addr() *Addr {
	return &Addr{CID: sa.CID, Port: sa.Port}
}

func sockErr(op string, errno unix.Errno) error {
	return &net.OpError{Op: op, Net: "vsock", Err: os.NewSyscallError(op, errno)}
}

func bind(fd int, sa *sockaddrVM) error {
	_, _, errno := unix.Syscall(unix.SYS_BIND, uintptr(fd), uintptr(unsafe.Pointer(sa)), uintptr(sockaddrVMLen))
	if errno != 0 {
		return sockErr("bind", errno)
	}

	return nil
}

func connect(fd int, sa *sockaddrVM) unix.Errno {
	_, _, errno := unix.Syscall(unix.SYS_CONNECT, uintptr(fd), uintptr(unsafe.Pointer(sa)), uintptr(sockaddrVMLen))
	return errno
}

func getsockname(fd int) *Addr {
	sa := sockaddrVM{}
	l := sockaddrVMLen
	_, _, errno := unix.Syscall(unix.SYS_GETSOCKNAME, uintptr(fd), uintptr(unsafe.Pointer(&sa)), uintptr(unsafe.Pointer(&l)))
	if errno != 0 {
		return &Addr{}
	}

	return sa.addr()
}

func getpeername(fd int) *Addr {
	sa := sockaddrVM{}
	l := sockaddrVMLen
	_, _, errno := unix.Syscall(unix.SYS_GETPEERNAME, uintptr(fd), uintptr(unsafe.Pointer(&sa)), uintptr(unsafe.Pointer(&l)))
	if errno != 0 {
		return &Addr{}
	}

	return sa.addr()
}

func newSocket() (int, error) {
	fd, err := unix.Socket(afVsock, unix.SOCK_STREAM|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, 0)
	if err != nil {
		return -1, fmt.Errorf("Failed creating vsock socket (is the vsock kernel module loaded?): %w", err)
	}

	return fd, nil
}

// conn is a net.Conn backed by a non-blocking socket file descriptor wrapped in
// an *os.File, which integrates it with the Go runtime poller.
type conn struct {
	f      *os.File
	local  net.Addr
	remote net.Addr
}

func newConn(fd int) *conn {
	// The fd is non-blocking, so os.NewFile registers it with the runtime poller.
	return &conn{
		f:      os.NewFile(uintptr(fd), "vsock"),
		local:  getsockname(fd),
		remote: getpeername(fd),
	}
}

var debug = os.Getenv("LXD_AGENT_VSOCK_DEBUG") != ""

func (c *conn) debugf(format string, args ...any) {
	if debug {
		fmt.Fprintf(os.Stderr, "vsock[%d %s]: %s\n", c.f.Fd(), c.remote, fmt.Sprintf(format, args...))
	}
}

// wrapErr converts an *os.File error into the *net.OpError a net.Conn is
// expected to return. Callers such as net/http type-assert errors to net.Error
// (for example to recognise the timeout they caused themselves by aborting a
// pending read with a deadline in the past), which *fs.PathError does not
// implement.
func (c *conn) wrapErr(op string, err error) error {
	if err == nil || errors.Is(err, io.EOF) {
		return err
	}

	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		err = pathErr.Err
	}

	if errors.Is(err, fs.ErrClosed) {
		err = net.ErrClosed
	}

	return &net.OpError{Op: op, Net: "vsock", Source: c.local, Addr: c.remote, Err: err}
}

func (c *conn) Read(b []byte) (int, error) {
	n, err := c.f.Read(b)
	if err != nil {
		c.debugf("read n=%d err=%v", n, err)
	}

	return n, c.wrapErr("read", err)
}

func (c *conn) Write(b []byte) (int, error) {
	n, err := c.f.Write(b)
	if err != nil {
		c.debugf("write n=%d err=%v", n, err)
	}

	return n, c.wrapErr("write", err)
}

func (c *conn) Close() error {
	c.debugf("close")
	return c.wrapErr("close", c.f.Close())
}

func (c *conn) LocalAddr() net.Addr  { return c.local }
func (c *conn) RemoteAddr() net.Addr { return c.remote }

func (c *conn) SetDeadline(t time.Time) error {
	c.debugf("SetDeadline %s", deadlineString(t))
	return c.wrapErr("set", c.f.SetDeadline(t))
}

func (c *conn) SetReadDeadline(t time.Time) error {
	c.debugf("SetReadDeadline %s", deadlineString(t))
	return c.wrapErr("set", c.f.SetReadDeadline(t))
}

func (c *conn) SetWriteDeadline(t time.Time) error {
	c.debugf("SetWriteDeadline %s", deadlineString(t))
	return c.wrapErr("set", c.f.SetWriteDeadline(t))
}

func deadlineString(t time.Time) string {
	if t.IsZero() {
		return "none"
	}

	return "in " + time.Until(t).Round(time.Millisecond).String()
}

// listener is a net.Listener on a vsock port.
type listener struct {
	fd     int
	addr   *Addr
	closed chan struct{}
	once   sync.Once
}

// Listen listens on the given port on any context ID (VMADDR_CID_ANY).
func Listen(port uint32) (net.Listener, error) {
	fd, err := newSocket()
	if err != nil {
		return nil, err
	}

	err = bind(fd, newSockaddr(CIDAny, port))
	if err != nil {
		_ = unix.Close(fd)
		return nil, err
	}

	err = unix.Listen(fd, 128)
	if err != nil {
		_ = unix.Close(fd)
		return nil, sockErr("listen", err.(unix.Errno))
	}

	return &listener{fd: fd, addr: &Addr{CID: CIDAny, Port: port}, closed: make(chan struct{})}, nil
}

// Accept waits for and returns the next connection.
func (l *listener) Accept() (net.Conn, error) {
	for {
		select {
		case <-l.closed:
			return nil, net.ErrClosed
		default:
		}

		nfd, _, errno := unix.Syscall6(unix.SYS_ACCEPT4, uintptr(l.fd), 0, 0, uintptr(unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK), 0, 0)
		switch errno {
		case 0:
			return newConn(int(nfd)), nil
		case unix.EINTR, unix.ECONNABORTED:
			continue
		case unix.EAGAIN:
			// Wait for a connection, waking up regularly to notice Close().
			fds := []unix.PollFd{{Fd: int32(l.fd), Events: unix.POLLIN}}
			_, err := unix.Poll(fds, 250)
			if err != nil && !errors.Is(err, unix.EINTR) {
				return nil, sockErr("accept", err.(unix.Errno))
			}
		default:
			return nil, sockErr("accept", errno)
		}
	}
}

// Close stops the listener.
func (l *listener) Close() error {
	var err error
	l.once.Do(func() {
		close(l.closed)
		err = unix.Close(l.fd)
	})

	return err
}

// Addr returns the listener's address.
func (l *listener) Addr() net.Addr {
	return l.addr
}

// DialContext connects to the given context ID and port.
func DialContext(ctx context.Context, cid uint32, port uint32) (net.Conn, error) {
	fd, err := newSocket()
	if err != nil {
		return nil, err
	}

	errno := connect(fd, newSockaddr(cid, port))
	if errno == unix.EINPROGRESS || errno == unix.EINTR {
		// Wait for the non-blocking connect to complete, honouring the context deadline.
		timeout := 10 * time.Second
		if deadline, ok := ctx.Deadline(); ok {
			timeout = time.Until(deadline)
		}

		if timeout <= 0 {
			_ = unix.Close(fd)
			return nil, context.DeadlineExceeded
		}

		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLOUT}}
		n, err := unix.Poll(fds, int(timeout/time.Millisecond))
		if err != nil {
			_ = unix.Close(fd)
			return nil, sockErr("connect", err.(unix.Errno))
		}

		if n == 0 {
			_ = unix.Close(fd)
			return nil, sockErr("connect", unix.ETIMEDOUT)
		}

		soErr, err := unix.GetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_ERROR)
		if err != nil {
			_ = unix.Close(fd)
			return nil, sockErr("connect", err.(unix.Errno))
		}

		errno = unix.Errno(soErr)
	}

	if errno != 0 {
		_ = unix.Close(fd)
		return nil, sockErr("connect", errno)
	}

	return newConn(fd), nil
}
