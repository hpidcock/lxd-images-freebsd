# vsock kernel modules

LXD talks to the agent inside a virtual machine over a virtio socket
(`AF_VSOCK`). FreeBSD's kernel has no vsock support yet; it is being worked
on by Danilo Egea Gondolfo (see the
[2024Q3 status report](https://www.freebsd.org/status/report-2024-07-2024-09/vsock/)).
Until it lands, the images built here ship his driver as two loadable kernel
modules built from the sources under `sys/`:

- `vsock.ko` – the `AF_VSOCK` protocol family (`sys/net/vsock.c`)
- `virtio_socket.ko` – the virtio-vsock transport (`sys/dev/virtio/socket/`)

They are copied from
<https://github.com/daniloegea/freebsd-src/tree/virtio_vsocks>
(commit `2b6c3df0bee33f7f94d061beeb753e96bf7998c4`, 2026-09-29), which is
BSD-2-Clause licensed; see the file headers. Local changes are marked with
`lxd-images-freebsd` comments:

- `virtio_socket.c`, `vtsock_send()`: the send buffer could be flushed by a
  reset from the peer between copying data out of it and dropping it, which
  made `sbdrop()` walk off the end of the mbuf chain and panic the kernel
  (`sbdrop+0x35 <- vtsock_send+0x16c`) when LXD closed connections while
  the agent was still writing. The size of the copy and the drop are now
  decided under the same lock hold.

The branch also adds `AF_VSOCK`/`PF_VSOCK` to `sys/sys/socket.h` and
`IOCTL_VM_SOCKETS_GET_LOCAL_CID` to `sys/sys/sockio.h`. The stock kernel does
not restrict protocol families registered by modules to `AF_MAX`, so the
modules work on unmodified release kernels as long as the agent uses the same
family number; `build.sh` appends the definitions to the extracted release
headers at build time, and `agent/internal/vsock/vsock_freebsd.go` hardcodes
the same value (48) and `struct sockaddr_vm` layout.

`build.sh` runs on FreeBSD and builds both modules against a release's
`src.txz`; `image/build.sh` runs it inside the builder VM for the release and
architecture of the image being built, and installs the result into
`/boot/modules` of the image.
