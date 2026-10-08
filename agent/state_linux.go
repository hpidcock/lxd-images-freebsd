//go:build linux

package main

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/shared/api"
)

func cpuState() api.InstanceStateCPU {
	cpu := api.InstanceStateCPU{}

	// Try cgroup v2.
	stats, err := os.ReadFile("/sys/fs/cgroup/cpu.stat")
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(stats))

		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())

			if fields[0] == "usage_usec" {
				valueInt, err := strconv.ParseInt(fields[1], 10, 64)
				if err != nil {
					cpu.Usage = -1
					return cpu
				}

				// usec -> nsec
				cpu.Usage = valueInt * 1000
				return cpu
			}
		}
	}

	// Try cgroup v1.
	value, err := os.ReadFile("/sys/fs/cgroup/cpuacct/cpuacct.usage")
	if err == nil {
		valueInt, err := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
		if err != nil {
			cpu.Usage = -1
			return cpu
		}

		cpu.Usage = valueInt

		return cpu
	}

	cpu.Usage = -1
	return cpu
}

func memoryUsage(stats metrics.MemoryMetrics) uint64 {
	return stats.MemTotalBytes - stats.MemFreeBytes
}

func memoryUsagePeak() int64 {
	// Memory peak in bytes
	value, err := os.ReadFile("/sys/fs/cgroup/memory/memory.max_usage_in_bytes")
	valueInt, err1 := strconv.ParseInt(strings.TrimSpace(string(value)), 10, 64)
	if err == nil && err1 == nil {
		return valueInt
	}

	return 0
}

func readSysNetCounter(iface string, name string) uint64 {
	value, err := os.ReadFile("/sys/class/net/" + iface + "/statistics/" + name)
	if err != nil {
		return 0
	}

	valueInt, err := strconv.ParseUint(strings.TrimSpace(string(value)), 10, 64)
	if err != nil {
		return 0
	}

	return valueInt
}

func networkCounters(iface net.Interface) api.InstanceStateNetworkCounters {
	return api.InstanceStateNetworkCounters{
		BytesSent:       readSysNetCounter(iface.Name, "tx_bytes"),
		BytesReceived:   readSysNetCounter(iface.Name, "rx_bytes"),
		PacketsSent:     readSysNetCounter(iface.Name, "tx_packets"),
		PacketsReceived: readSysNetCounter(iface.Name, "rx_packets"),
	}
}

func processesState() int64 {
	pids := []int64{1}

	// Go through the pid list, adding new pids at the end so we go through them all
	for i := range pids {
		pid := strconv.FormatInt(pids[i], 10)
		fname := "/proc/" + pid + "/task/" + pid + "/children"
		fcont, err := os.ReadFile(fname)
		if err != nil {
			// the process terminated during execution of this loop
			continue
		}

		content := strings.Split(string(fcont), " ")
		for j := range content {
			pid, err := strconv.ParseInt(content[j], 10, 64)
			if err == nil {
				pids = append(pids, pid)
			}
		}
	}

	return int64(len(pids))
}
