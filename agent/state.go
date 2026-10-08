package main

import (
	"math"
	"net"
	"net/http"
	"strings"

	"github.com/canonical/lxd/shared"
	"github.com/canonical/lxd/shared/api"
	"github.com/canonical/lxd/shared/logger"

	"github.com/hpidcock/lxd-images-freebsd/agent/internal/response"
)

var stateCmd = APIEndpoint{
	Name: "state",
	Path: "state",

	Get: APIEndpointAction{Handler: stateGet},
	Put: APIEndpointAction{Handler: statePut},
}

func stateGet(d *Daemon, r *http.Request) response.Response {
	return response.SyncResponse(true, renderState())
}

func statePut(d *Daemon, r *http.Request) response.Response {
	return response.NotImplemented(nil)
}

func renderState() *api.InstanceState {
	return &api.InstanceState{
		CPU:       cpuState(),
		Memory:    memoryState(),
		Network:   networkState(),
		Pid:       1,
		Processes: processesState(),
	}
}

func memoryState() api.InstanceStateMemory {
	memory := api.InstanceStateMemory{}

	stats, err := getMemoryMetrics()
	if err != nil {
		return memory
	}

	// Bound checking before converting from uint64 to int64
	if stats.MemTotalBytes > math.MaxInt64 {
		memory.Total = math.MaxInt64
	} else {
		memory.Total = int64(stats.MemTotalBytes)
	}

	usage := memoryUsage(stats)
	if usage > math.MaxInt64 {
		memory.Usage = math.MaxInt64
	} else {
		memory.Usage = int64(usage)
	}

	memory.UsagePeak = memoryUsagePeak()

	return memory
}

func networkState() map[string]api.InstanceStateNetwork {
	result := map[string]api.InstanceStateNetwork{}

	ifs, err := net.Interfaces()
	if err != nil {
		logger.Errorf("Failed retrieving network interfaces: %v", err)
		return result
	}

	for _, iface := range ifs {
		network := api.InstanceStateNetwork{
			Addresses: []api.InstanceStateNetworkAddress{},
			Counters:  networkCounters(iface),
		}

		network.Hwaddr = iface.HardwareAddr.String()
		network.Mtu = iface.MTU

		if iface.Flags&net.FlagUp != 0 {
			network.State = "up"
		} else {
			network.State = "down"
		}

		if iface.Flags&net.FlagBroadcast != 0 {
			network.Type = "broadcast"
		} else if iface.Flags&net.FlagLoopback != 0 {
			network.Type = "loopback"
		} else if iface.Flags&net.FlagPointToPoint != 0 {
			network.Type = "point-to-point"
		} else {
			network.Type = "unknown"
		}

		// Addresses
		addrs, _ := iface.Addrs()

		for _, addr := range addrs {
			address, netmask, found := strings.Cut(addr.String(), "/")
			if !found {
				continue
			}

			networkAddress := api.InstanceStateNetworkAddress{
				Address: address,
				Family:  "inet",
				Netmask: netmask,
				Scope:   shared.GetIPScope(address),
			}

			if strings.Contains(address, ":") {
				networkAddress.Family = "inet6"
			}

			network.Addresses = append(network.Addresses, networkAddress)
		}

		result[iface.Name] = network
	}

	return result
}
