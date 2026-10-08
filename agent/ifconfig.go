package main

import (
	"context"
	"strconv"

	"github.com/canonical/lxd/shared"
)

// ifconfigLink drives a network interface with ifconfig(8).
type ifconfigLink struct {
	Name string
}

func (l *ifconfigLink) run(args ...string) error {
	_, err := shared.RunCommand(context.TODO(), "ifconfig", append([]string{l.Name}, args...)...)
	return err
}

// SetUp brings the interface up.
func (l *ifconfigLink) SetUp() error {
	return l.run("up")
}

// SetDown brings the interface down.
func (l *ifconfigLink) SetDown() error {
	return l.run("down")
}

// SetName renames the interface.
func (l *ifconfigLink) SetName(name string) error {
	return l.run("name", name)
}

// SetMTU sets the interface MTU.
func (l *ifconfigLink) SetMTU(mtu uint32) error {
	return l.run("mtu", strconv.FormatUint(uint64(mtu), 10))
}
