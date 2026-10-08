// vsocktest is a small development tool to exercise the vsock package inside a
// FreeBSD VM: it echoes what it receives and logs every event with timings.
//
//	vsocktest listen PORT         accept connections, echo data, log EOF/errors
//	vsocktest dial CID PORT MSG   connect, send MSG, print the reply
//
// From a Linux host: socat - VSOCK-CONNECT:<cid>:<port>
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/vsock"
)

func logf(format string, args ...any) {
	fmt.Printf(time.Now().Format("15:04:05.000")+" "+format+"\n", args...)
}

func serve(c net.Conn) {
	defer c.Close()
	logf("accepted %s -> %s", c.RemoteAddr(), c.LocalAddr())

	// Exercise deadlines: set one, then clear it, as net/http does after reading headers.
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_ = c.SetReadDeadline(time.Time{})

	buf := make([]byte, 65536)
	total := 0
	for {
		n, err := c.Read(buf)
		if n > 0 {
			total += n
			logf("read %d bytes (total %d)", n, total)
			_, werr := c.Write(buf[:n])
			if werr != nil {
				logf("write error: %v", werr)
				return
			}
		}

		if err != nil {
			if err == io.EOF {
				logf("EOF after %d bytes", total)
			} else {
				logf("read error: %v", err)
			}

			return
		}
	}
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: vsocktest listen PORT | dial CID PORT MSG")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "listen":
		port, _ := strconv.ParseUint(os.Args[2], 10, 32)
		l, err := vsock.Listen(uint32(port))
		if err != nil {
			logf("listen: %v", err)
			os.Exit(1)
		}

		logf("listening on %s", l.Addr())
		for {
			c, err := l.Accept()
			if err != nil {
				logf("accept: %v", err)
				os.Exit(1)
			}

			go serve(c)
		}
	case "deadline":
		// Reproduce net/http's pattern: set a read deadline, let it expire,
		// clear it, then block in Read. The read must wait for data.
		port, _ := strconv.ParseUint(os.Args[2], 10, 32)
		l, err := vsock.Listen(uint32(port))
		if err != nil {
			logf("listen: %v", err)
			os.Exit(1)
		}

		logf("listening on %s", l.Addr())
		for {
			c, err := l.Accept()
			if err != nil {
				logf("accept: %v", err)
				os.Exit(1)
			}

			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 4096)
				logf("accepted %s", c.RemoteAddr())
				_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
				n, err := c.Read(buf)
				logf("read with 500ms deadline: n=%d err=%v", n, err)
				_ = c.SetReadDeadline(time.Time{})
				logf("deadline cleared, reading (should block until data)")
				start := time.Now()
				n, err = c.Read(buf)
				logf("read after clear: n=%d err=%v after %s", n, err, time.Since(start).Round(time.Millisecond))
				_ = c.SetDeadline(time.Now().Add(300 * time.Millisecond))
				_ = c.SetDeadline(time.Time{})
				start = time.Now()
				n, err = c.Read(buf)
				logf("read after set+clear: n=%d err=%v after %s", n, err, time.Since(start).Round(time.Millisecond))
			}(c)
		}
	case "dial":
		cid, _ := strconv.ParseUint(os.Args[2], 10, 32)
		port, _ := strconv.ParseUint(os.Args[3], 10, 32)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		c, err := vsock.DialContext(ctx, uint32(cid), uint32(port))
		if err != nil {
			logf("dial: %v", err)
			os.Exit(1)
		}

		logf("connected %s -> %s", c.LocalAddr(), c.RemoteAddr())
		msg := "ping"
		if len(os.Args) > 4 {
			msg = os.Args[4]
		}

		_, err = c.Write([]byte(msg + "\n"))
		if err != nil {
			logf("write: %v", err)
			os.Exit(1)
		}

		buf := make([]byte, 65536)
		_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, err := c.Read(buf)
		logf("reply %q err=%v", string(buf[:n]), err)
		_ = c.Close()
	}
}
