//go:build freebsd

// Package sysinfo reads CPU, memory, process and network statistics from the
// FreeBSD kernel through sysctl(3).
package sysinfo

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// cpuStates is CPUSTATES from sys/resource.h; the order is CP_USER, CP_NICE, CP_SYS, CP_INTR, CP_IDLE.
const cpuStates = 5

// CPUTimes holds CPU time in seconds.
type CPUTimes struct {
	User      float64
	Nice      float64
	System    float64
	Interrupt float64
	Idle      float64
}

// clockinfo mirrors struct clockinfo from sys/time.h.
type clockinfo struct {
	Hz     int32
	Tick   int32
	Spare  int32
	Stathz int32
	Profhz int32
}

// statHz returns the statistics clock frequency the kern.cp_time counters tick at.
func statHz() (float64, error) {
	raw, err := unix.SysctlRaw("kern.clockrate")
	if err != nil {
		return 0, err
	}

	var ci clockinfo
	if len(raw) < int(unsafe.Sizeof(ci)) {
		return 0, errors.New("Short kern.clockrate")
	}

	err = binary.Read(bytes.NewReader(raw), binary.LittleEndian, &ci)
	if err != nil {
		return 0, err
	}

	hz := ci.Stathz
	if hz == 0 {
		hz = ci.Hz
	}

	if hz <= 0 {
		return 0, errors.New("Invalid kern.clockrate")
	}

	return float64(hz), nil
}

func ticksToTimes(ticks []uint64, hz float64) CPUTimes {
	return CPUTimes{
		User:      float64(ticks[0]) / hz,
		Nice:      float64(ticks[1]) / hz,
		System:    float64(ticks[2]) / hz,
		Interrupt: float64(ticks[3]) / hz,
		Idle:      float64(ticks[4]) / hz,
	}
}

func readTicks(name string) ([]uint64, error) {
	raw, err := unix.SysctlRaw(name)
	if err != nil {
		return nil, err
	}

	if len(raw)%8 != 0 {
		return nil, fmt.Errorf("Unexpected %s size %d", name, len(raw))
	}

	ticks := make([]uint64, len(raw)/8)
	for i := range ticks {
		ticks[i] = binary.LittleEndian.Uint64(raw[i*8:])
	}

	return ticks, nil
}

// CPUTime returns the aggregate CPU times of the system.
func CPUTime() (CPUTimes, error) {
	hz, err := statHz()
	if err != nil {
		return CPUTimes{}, err
	}

	ticks, err := readTicks("kern.cp_time")
	if err != nil {
		return CPUTimes{}, err
	}

	if len(ticks) < cpuStates {
		return CPUTimes{}, errors.New("Short kern.cp_time")
	}

	return ticksToTimes(ticks, hz), nil
}

// PerCPUTimes returns the CPU times of every online CPU.
func PerCPUTimes() ([]CPUTimes, error) {
	hz, err := statHz()
	if err != nil {
		return nil, err
	}

	ticks, err := readTicks("kern.cp_times")
	if err != nil {
		return nil, err
	}

	ncpu, err := unix.SysctlUint32("hw.ncpu")
	if err != nil {
		return nil, err
	}

	cpus := make([]CPUTimes, 0, ncpu)
	for i := 0; i < int(ncpu) && (i+1)*cpuStates <= len(ticks); i++ {
		cpus = append(cpus, ticksToTimes(ticks[i*cpuStates:(i+1)*cpuStates], hz))
	}

	return cpus, nil
}

// Memory holds memory statistics in bytes.
type Memory struct {
	Total     uint64
	Free      uint64
	Available uint64
	Active    uint64
	Inactive  uint64
	Wired     uint64
	Laundry   uint64
	Buffers   uint64
	SwapTotal uint64
	SwapUsed  uint64
}

func pages(name string, pageSize uint64) uint64 {
	v, err := unix.SysctlUint32(name)
	if err != nil {
		return 0
	}

	return uint64(v) * pageSize
}

// MemoryStats returns memory statistics.
func MemoryStats() (Memory, error) {
	pageSize, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return Memory{}, err
	}

	ps := uint64(pageSize)

	total, err := unix.SysctlUint64("hw.physmem")
	if err != nil {
		return Memory{}, err
	}

	m := Memory{
		Total:    total,
		Free:     pages("vm.stats.vm.v_free_count", ps),
		Active:   pages("vm.stats.vm.v_active_count", ps),
		Inactive: pages("vm.stats.vm.v_inactive_count", ps),
		Wired:    pages("vm.stats.vm.v_wire_count", ps),
		Laundry:  pages("vm.stats.vm.v_laundry_count", ps),
	}

	bufspace, err := unix.SysctlUint64("vfs.bufspace")
	if err == nil {
		m.Buffers = bufspace
	}

	// Free, inactive and laundry pages can all be reclaimed.
	m.Available = m.Free + m.Inactive + m.Laundry

	swapTotal, err := unix.SysctlUint64("vm.swap_total")
	if err == nil {
		m.SwapTotal = swapTotal
	}

	return m, nil
}

// ProcessCount returns the number of processes.
func ProcessCount() (int, error) {
	// KERN_PROC_PROC returns one kinfo_proc per process (no threads).
	raw, err := unix.SysctlRaw("kern.proc.proc")
	if err != nil {
		return 0, err
	}

	if len(raw) < 4 {
		return 0, nil
	}

	// ki_structsize is the first field of struct kinfo_proc.
	structSize := int(binary.LittleEndian.Uint32(raw))
	if structSize <= 0 {
		return 0, errors.New("Invalid kinfo_proc size")
	}

	return len(raw) / structSize, nil
}

// IfData mirrors struct if_data from net/if.h.
type IfData struct {
	Type       uint8
	Physical   uint8
	Addrlen    uint8
	Hdrlen     uint8
	LinkState  uint8
	Vhid       uint8
	Datalen    uint16
	Mtu        uint32
	Metric     uint32
	Baudrate   uint64
	Ipackets   uint64
	Ierrors    uint64
	Opackets   uint64
	Oerrors    uint64
	Collisions uint64
	Ibytes     uint64
	Obytes     uint64
	Imcasts    uint64
	Omcasts    uint64
	Iqdrops    uint64
	Oqdrops    uint64
	Noproto    uint64
	Hwassist   uint64
	Epoch      uint64
	Lastchange [2]uint64
}

// ifmibData mirrors struct ifmibdata from net/if_mib.h.
type ifmibData struct {
	Name      [16]byte
	Pcount    int32
	Flags     int32
	SndLen    int32
	SndMaxlen int32
	SndDrops  int32
	Filler    [4]int32
	_         [4]byte // encoding/binary does not pad; if_data is 8-byte aligned in C.
	Data      IfData
}

// InterfaceData returns the if_data statistics of the interface with the given index.
func InterfaceData(index int) (*IfData, error) {
	// net.link.generic.ifdata.<index>.IFDATA_GENERAL
	raw, err := unix.SysctlRaw("net.link.generic.ifdata", index, 1)
	if err != nil {
		return nil, err
	}

	var mib ifmibData
	if len(raw) < int(unsafe.Sizeof(mib)) {
		return nil, fmt.Errorf("Short ifmibdata for interface %d", index)
	}

	err = binary.Read(bytes.NewReader(raw), binary.LittleEndian, &mib)
	if err != nil {
		return nil, err
	}

	return &mib.Data, nil
}
