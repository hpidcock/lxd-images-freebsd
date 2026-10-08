//go:build freebsd

package main

import (
	"strconv"

	"github.com/canonical/lxd/lxd/metrics"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/sysinfo"
)

func getCPUMetrics() (map[string]metrics.CPUMetrics, error) {
	cpus, err := sysinfo.PerCPUTimes()
	if err != nil {
		return nil, err
	}

	out := make(map[string]metrics.CPUMetrics, len(cpus))
	for i, cpu := range cpus {
		out["cpu"+strconv.Itoa(i)] = metrics.CPUMetrics{
			SecondsUser:   cpu.User,
			SecondsNice:   cpu.Nice,
			SecondsSystem: cpu.System,
			SecondsIdle:   cpu.Idle,
			SecondsIRQ:    cpu.Interrupt,
		}
	}

	return out, nil
}

func getTotalProcesses() (uint64, error) {
	n, err := sysinfo.ProcessCount()
	if err != nil {
		return 0, err
	}

	return uint64(n), nil
}

// getDiskMetrics is not implemented on FreeBSD (it would need to parse kern.devstat.all).
func getDiskMetrics() (map[string]metrics.DiskMetrics, error) {
	return map[string]metrics.DiskMetrics{}, nil
}

func getMemoryMetrics() (metrics.MemoryMetrics, error) {
	m, err := sysinfo.MemoryStats()
	if err != nil {
		return metrics.MemoryMetrics{}, err
	}

	out := metrics.MemoryMetrics{
		MemTotalBytes:     m.Total,
		MemFreeBytes:      m.Free,
		MemAvailableBytes: m.Available,
		ActiveBytes:       m.Active,
		InactiveBytes:     m.Inactive,
		UnevictableBytes:  m.Wired,
		CachedBytes:       m.Buffers,
	}

	if out.MemTotalBytes > out.MemAvailableBytes {
		out.RSSBytes = out.MemTotalBytes - out.MemAvailableBytes
	}

	return out, nil
}
