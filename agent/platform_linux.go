//go:build linux

package main

// vsockModules are the kernel modules providing AF_VSOCK over virtio on Linux.
var vsockModules = []string{"vsock"}

// syslogName is the syslog identity the agent logs under.
const syslogName = "lxd-agent"
