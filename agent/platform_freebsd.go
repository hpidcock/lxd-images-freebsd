//go:build freebsd

package main

// vsockModules are the kernel modules providing AF_VSOCK over virtio on
// FreeBSD. They are built from kmod/ and installed into /boot/modules.
var vsockModules = []string{"vsock", "virtio_socket"}

// syslogName is empty because upstream's logger only supports syslog on Linux; the
// rc script captures the agent's stderr in /var/log/lxd-agent.log instead.
const syslogName = ""
