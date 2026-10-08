//go:build linux

package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/canonical/lxd/lxd/metrics"
)

func getCPUMetrics() (map[string]metrics.CPUMetrics, error) {
	stats, err := os.ReadFile("/proc/stat")
	if err != nil {
		return nil, fmt.Errorf("Failed reading /proc/stat: %w", err)
	}

	out := map[string]metrics.CPUMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(stats))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		// Only consider CPU info, skip everything else. Skip aggregated CPU stats since there will
		// be stats for each individual CPU.
		if !strings.HasPrefix(fields[0], "cpu") || fields[0] == "cpu" {
			continue
		}

		// Validate the number of fields only for lines starting with "cpu".
		if len(fields) < 9 {
			return nil, fmt.Errorf("Invalid /proc/stat content: %q", line)
		}

		values := make([]float64, 8)
		for i := range values {
			values[i], err = strconv.ParseFloat(fields[i+1], 64)
			if err != nil {
				return nil, fmt.Errorf("Failed parsing %q: %w", fields[i+1], err)
			}

			values[i] /= 100
		}

		out[fields[0]] = metrics.CPUMetrics{
			SecondsUser:    values[0],
			SecondsNice:    values[1],
			SecondsSystem:  values[2],
			SecondsIdle:    values[3],
			SecondsIOWait:  values[4],
			SecondsIRQ:     values[5],
			SecondsSoftIRQ: values[6],
			SecondsSteal:   values[7],
		}
	}

	if scanner.Err() != nil {
		return nil, fmt.Errorf("Failed scanning /proc/stat: %w", scanner.Err())
	}

	return out, nil
}

func getTotalProcesses() (uint64, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0, fmt.Errorf("Failed reading dir %q: %w", "/proc", err)
	}

	pidCount := uint64(0)

	for _, entry := range entries {
		// Skip everything which isn't a directory
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()

		// Skip all non-PID directories
		_, err := strconv.ParseUint(name, 10, 64)
		if err != nil {
			continue
		}

		cmdlinePath := filepath.Join("/proc", name, "cmdline")

		cmdline, err := os.ReadFile(cmdlinePath)
		if err != nil {
			continue
		}

		if string(cmdline) == "" {
			continue
		}

		pidCount++
	}

	return pidCount, nil
}

func getDiskMetrics() (map[string]metrics.DiskMetrics, error) {
	diskStats, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, fmt.Errorf("Failed reading /proc/diskstats: %w", err)
	}

	out := map[string]metrics.DiskMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(diskStats))

	for scanner.Scan() {
		line := scanner.Text()

		if line == "" {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 10 {
			return nil, fmt.Errorf("Invalid /proc/diskstats content: %q", line)
		}

		stats := metrics.DiskMetrics{}

		stats.ReadsCompleted, err = strconv.ParseUint(fields[3], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing %q: %w", fields[3], err)
		}

		sectorsRead, err := strconv.ParseUint(fields[5], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing %q: %w", fields[5], err)
		}

		stats.ReadBytes = sectorsRead * 512

		stats.WritesCompleted, err = strconv.ParseUint(fields[7], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing %q: %w", fields[7], err)
		}

		sectorsWritten, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("Failed parsing %q: %w", fields[9], err)
		}

		stats.WrittenBytes = sectorsWritten * 512

		out[fields[2]] = stats
	}

	if scanner.Err() != nil {
		return nil, fmt.Errorf("Failed scanning /proc/diskstats: %w", scanner.Err())
	}

	return out, nil
}

func getMemoryMetrics() (metrics.MemoryMetrics, error) {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return metrics.MemoryMetrics{}, fmt.Errorf("Failed reading /proc/meminfo: %w", err)
	}

	out := metrics.MemoryMetrics{}
	scanner := bufio.NewScanner(bytes.NewReader(content))

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)

		if len(fields) < 2 {
			return metrics.MemoryMetrics{}, fmt.Errorf("Invalid /proc/meminfo content: %q", line)
		}

		fields[0] = strings.TrimRight(fields[0], ":")

		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return metrics.MemoryMetrics{}, fmt.Errorf("Failed parsing %q: %w", fields[1], err)
		}

		// Multiply suffix (kB)
		if len(fields) == 3 {
			value *= 1024
		}

		// Parse fields for metrics collection
		switch fields[0] {
		case "Active":
			out.ActiveBytes = value
		case "Active(anon)":
			out.ActiveAnonBytes = value
		case "Active(file)":
			out.ActiveFileBytes = value
		case "Cached":
			out.CachedBytes = value
		case "Dirty":
			out.DirtyBytes = value
		case "HugePages_Free":
			out.HugepagesFreeBytes = value
		case "HugePages_Total":
			out.HugepagesTotalBytes = value
		case "Inactive":
			out.InactiveBytes = value
		case "Inactive(anon)":
			out.InactiveAnonBytes = value
		case "Inactive(file)":
			out.InactiveFileBytes = value
		case "Mapped":
			out.MappedBytes = value
		case "MemAvailable":
			out.MemAvailableBytes = value
		case "MemFree":
			out.MemFreeBytes = value
		case "MemTotal":
			out.MemTotalBytes = value
		case "Shmem":
			out.ShmemBytes = value
		case "SReclaimable":
			out.SReclaimableBytes = value
		case "SwapCached":
			out.SwapBytes = value
		case "Unevictable":
			out.UnevictableBytes = value
		case "Writeback":
			out.WritebackBytes = value
		}
	}

	if out.MemTotalBytes > out.MemAvailableBytes {
		out.RSSBytes = out.MemTotalBytes - out.MemAvailableBytes
	}

	if scanner.Err() != nil {
		return metrics.MemoryMetrics{}, fmt.Errorf("Failed scanning /proc/meminfo: %w", scanner.Err())
	}

	return out, nil
}
