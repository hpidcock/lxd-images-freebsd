//go:build freebsd

package main

import (
	"net"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/shared/api"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/sysinfo"
)

func cpuState() api.InstanceStateCPU {
	cpu := api.InstanceStateCPU{Usage: -1}

	times, err := sysinfo.CPUTime()
	if err != nil {
		return cpu
	}

	// Everything but idle counts as usage, in nanoseconds.
	cpu.Usage = int64((times.User + times.Nice + times.System + times.Interrupt) * 1e9)
	return cpu
}

func memoryUsage(stats metrics.MemoryMetrics) uint64 {
	// On FreeBSD inactive pages are reclaimable, so report what is not available rather than what is not free.
	if stats.MemAvailableBytes > 0 && stats.MemTotalBytes > stats.MemAvailableBytes {
		return stats.MemTotalBytes - stats.MemAvailableBytes
	}

	return stats.MemTotalBytes - stats.MemFreeBytes
}

func memoryUsagePeak() int64 {
	return 0
}

func networkCounters(iface net.Interface) api.InstanceStateNetworkCounters {
	data, err := sysinfo.InterfaceData(iface.Index)
	if err != nil {
		return api.InstanceStateNetworkCounters{}
	}

	return api.InstanceStateNetworkCounters{
		BytesReceived:          data.Ibytes,
		BytesSent:              data.Obytes,
		PacketsReceived:        data.Ipackets,
		PacketsSent:            data.Opackets,
		ErrorsReceived:         data.Ierrors,
		ErrorsSent:             data.Oerrors,
		PacketsDroppedInbound:  data.Iqdrops,
		PacketsDroppedOutbound: data.Oqdrops,
	}
}

func processesState() int64 {
	n, err := sysinfo.ProcessCount()
	if err != nil {
		return -1
	}

	return int64(n)
}
