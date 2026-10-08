package sysutil

import (
	"golang.org/x/sys/unix"
)

// Utsname returns the same info as unix.Utsname, as strings.
type Utsname struct {
	Sysname  string
	Nodename string
	Release  string
	Version  string
	Machine  string
}

// Uname returns Utsname as strings.
func Uname() (*Utsname, error) {
	uname := unix.Utsname{}
	err := unix.Uname(&uname)
	if err != nil {
		return nil, err
	}

	return &Utsname{
		Sysname:  unix.ByteSliceToString(uname.Sysname[:]),
		Nodename: unix.ByteSliceToString(uname.Nodename[:]),
		Release:  unix.ByteSliceToString(uname.Release[:]),
		Version:  unix.ByteSliceToString(uname.Version[:]),
		Machine:  unix.ByteSliceToString(uname.Machine[:]),
	}, nil
}
