//go:build freebsd

// sysinfotest prints what the sysinfo package reads from the FreeBSD kernel, for
// checking the agent's state and metrics against netstat/top inside a VM.
package main

import (
	"encoding/hex"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/sysinfo"
)

func main() {
	t, err := sysinfo.CPUTime()
	fmt.Printf("cpu time: %+v err=%v\n", t, err)
	cpus, err := sysinfo.PerCPUTimes()
	fmt.Printf("per cpu: %d err=%v %+v\n", len(cpus), err, cpus)
	m, err := sysinfo.MemoryStats()
	fmt.Printf("memory: %+v err=%v\n", m, err)
	n, err := sysinfo.ProcessCount()
	fmt.Printf("processes: %d err=%v\n", n, err)

	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		raw, err := unix.SysctlRaw("net.link.generic.ifdata", i.Index, 1)
		fmt.Printf("%s index=%d raw=%d bytes err=%v\n", i.Name, i.Index, len(raw), err)
		if len(os.Args) > 1 && err == nil {
			fmt.Print(hex.Dump(raw))
		}

		d, err := sysinfo.InterfaceData(i.Index)
		if err == nil {
			fmt.Printf("  ipackets=%d ibytes=%d opackets=%d obytes=%d ierrors=%d iqdrops=%d mtu=%d datalen=%d\n", d.Ipackets, d.Ibytes, d.Opackets, d.Obytes, d.Ierrors, d.Iqdrops, d.Mtu, d.Datalen)
		} else {
			fmt.Printf("  err=%v\n", err)
		}
	}
}
