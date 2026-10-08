package main

import (
	"net/http"
	"regexp"
	"slices"

	"github.com/canonical/lxd/lxd/metrics"
	"github.com/canonical/lxd/shared/logger"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/filesystem"
	"github.com/hpidcock/lxd-images-freebsd/agent/internal/response"
)

// These mountpoints are excluded as they are irrelevant for metrics.
// /var/lib/docker/* subdirectories are excluded for this reason: https://github.com/prometheus/node_exporter/pull/1003
var defMountPointsExcluded = regexp.MustCompile(`^/(?:dev|proc|sys|var/lib/docker/.+)(?:$|/)`)
var defFSTypesExcluded = []string{
	"autofs", "binfmt_misc", "bpf", "cgroup", "cgroup2", "configfs", "debugfs", "devfs", "devpts", "devtmpfs", "fdescfs", "fusectl", "fuse.lxcfs", "hugetlbfs", "iso9660", "linprocfs", "linsysfs", "mqueue", "nsfs", "overlay", "proc", "procfs", "pstore", "rpc_pipefs", "securityfs", "selinuxfs", "squashfs", "sysfs", "tracefs"}

var metricsCmd = APIEndpoint{
	Path: "metrics",

	Get: APIEndpointAction{Handler: metricsGet},
}

func metricsGet(d *Daemon, r *http.Request) response.Response {
	out := metrics.Metrics{}

	diskStats, err := getDiskMetrics()
	if err != nil {
		logger.Warn("Failed getting disk metrics", logger.Ctx{"err": err})
	} else {
		out.Disk = diskStats
	}

	filesystemStats, err := getFilesystemMetrics()
	if err != nil {
		logger.Warn("Failed getting filesystem metrics", logger.Ctx{"err": err})
	} else {
		out.Filesystem = filesystemStats
	}

	memStats, err := getMemoryMetrics()
	if err != nil {
		logger.Warn("Failed getting memory metrics", logger.Ctx{"err": err})
	} else {
		out.Memory = memStats
	}

	netStats, err := getNetworkMetrics()
	if err != nil {
		logger.Warn("Failed getting network metrics", logger.Ctx{"err": err})
	} else {
		out.Network = netStats
	}

	out.ProcessesTotal, err = getTotalProcesses()
	if err != nil {
		logger.Warn("Failed getting total processes", logger.Ctx{"err": err})
	}

	cpuStats, err := getCPUMetrics()
	if err != nil {
		logger.Warn("Failed getting CPU metrics", logger.Ctx{"err": err})
	} else {
		out.CPU = cpuStats
	}

	return response.SyncResponse(true, &out)
}

func getFilesystemMetrics() (map[string]metrics.FilesystemMetrics, error) {
	mounts, err := filesystem.Mounts()
	if err != nil {
		return nil, err
	}

	out := map[string]metrics.FilesystemMetrics{}

	for _, mount := range mounts {
		// Skip uninteresting mounts
		if slices.Contains(defFSTypesExcluded, mount.FSType) || defMountPointsExcluded.MatchString(mount.Mountpoint) {
			continue
		}

		stats := metrics.FilesystemMetrics{}

		stats.Mountpoint = mount.Mountpoint
		stats.FSType = mount.FSType

		stats.AvailableBytes = uint64(mount.Stat.Bavail) * uint64(mount.Stat.Bsize)
		stats.FreeBytes = uint64(mount.Stat.Bfree) * uint64(mount.Stat.Bsize)
		stats.SizeBytes = uint64(mount.Stat.Blocks) * uint64(mount.Stat.Bsize)

		out[mount.Source] = stats
	}

	return out, nil
}

func getNetworkMetrics() (map[string]metrics.NetworkMetrics, error) {
	out := map[string]metrics.NetworkMetrics{}

	for dev, state := range networkState() {
		stats := metrics.NetworkMetrics{}

		stats.ReceiveBytes = state.Counters.BytesReceived
		stats.ReceiveDrop = state.Counters.PacketsDroppedInbound
		stats.ReceiveErrors = state.Counters.ErrorsReceived
		stats.ReceivePackets = state.Counters.PacketsReceived
		stats.TransmitBytes = state.Counters.BytesSent
		stats.TransmitDrop = state.Counters.PacketsDroppedOutbound
		stats.TransmitErrors = state.Counters.ErrorsSent
		stats.TransmitPackets = state.Counters.PacketsSent

		out[dev] = stats
	}

	return out, nil
}
