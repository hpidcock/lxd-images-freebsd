# lxd-agent for FreeBSD

A port of LXD's virtual machine agent to FreeBSD, installed into the images
built by this repository as `/usr/local/bin/lxd-agent`.

## Provenance

The code in this directory started as a copy of `lxd-agent/` from
[canonical/lxd](https://github.com/canonical/lxd) at the commit pinned in
`go.mod` (the `github.com/canonical/lxd` requirement), which it still depends
on for everything that builds on FreeBSD: the API types, the client, the
events and operations machinery, certificates, websockets and so on. It is
licensed like LXD's `lxd-agent`, under the AGPL-3.0 (see `LICENSE`).

Only the Linux-only pieces are replaced:

| Upstream                                  | Here                                   | Notes |
| ----------------------------------------- | -------------------------------------- | ----- |
| `github.com/mdlayher/vsock`, `lxd/vsock`  | `internal/vsock`                       | `AF_VSOCK` sockets on the raw syscalls (the kernel side is `kmod/`); wraps the fd in an `*os.File` so the runtime poller handles deadlines, and returns `*net.OpError`s like a `net.Conn` must |
| `shared.OpenPty`, `SetSize`, `ExitStatus`, `NewExecWrapper`, `Uname` | `internal/sysutil` | `posix_openpt`/`FIODGNAME` based ptys for `lxc exec` |
| `lxd/util.LoadModule` (modprobe)          | `sysutil.LoadModule` (kldload)         | checks by file name with `kldstat -n` |
| `lxd/storage/filesystem`                  | `internal/filesystem`                  | `getfsstat`/`statfs` based |
| `lxd/response`, `lxd/util` (pull in Linux-only packages) | `internal/response`, `internal/util` | copies with the `ucred` dependency removed |
| `lxd/ip` (netlink)                        | `ifconfig.go`                          | NIC rename/MTU from LXD's `nics/` config via ifconfig(8) |
| `/proc` and cgroup based state/metrics    | `*_freebsd.go`, `internal/sysinfo`     | `kern.cp_time(s)`, `vm.stats`, `kern.proc.proc`, `net.link.generic.ifdata` sysctls; no disk I/O metrics |
| `/dev/lxd/sock`                           | `/var/run/lxd/sock`                    | devfs cannot hold directories |
| `/dev/virtio-ports/com.canonical.lxd`     | `/dev/vtcon/com.canonical.lxd`         | agent status ring buffer LXD reads; output processing is disabled on the tty |
| systemd units / `install.sh` from the config drive | `image/overlay/usr/local/etc/rc.d/lxd_agent` | mounts the 9p `config` share with p9fs, copies it to a tmpfs and starts the agent |

Hot-plugged disk devices use virtiofs on the LXD side, which FreeBSD does not
have, so those mounts fail with a logged error. Templates (`files/`), host
share mounts (`agent-mounts.json`) with 9p, the events API, operations, sftp
(`lxc file`), exec (`lxc exec`, with and without a pty) and devlxd work.

## cloud-init

`lxd-agent cloud-init` (`cloudinit/`) is new: it fetches the instance's
cloud-init data from devlxd and applies it with FreeBSD's nuageinit(7). The
`lxd_cloudinit*` rc scripts in `image/overlay/` drive it; see the top-level
README for what is supported.

## Building and testing

```sh
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build -tags agent,netgo -trimpath -ldflags "-s -w" -o lxd-agent .
go vet -tags agent,netgo ./...   # also with GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0
go test -tags agent,netgo ./...
```

The build must be static (`CGO_ENABLED=0`); nothing here uses cgo. The Linux
build exists so the package compiles, vets and tests on a development host;
the agent is only meant to run on FreeBSD.

Two development tools live under `cmd/` (FreeBSD only):

- `vsocktest` – echo server / dialer / read-deadline experiment over vsock.
  Pair it with `socat - VSOCK-CONNECT:<cid>:<port>` on the host, where
  `<cid>` is the instance's `volatile.vsock_id`.
- `sysinfotest` – prints what `internal/sysinfo` reads, next to the raw
  sysctl data, to compare with `netstat -ibn`, `top` and `ps`.

Setting `LXD_AGENT_VSOCK_DEBUG=1` in the agent's environment (for example
`service -E LXD_AGENT_VSOCK_DEBUG=1 lxd_agent start`) logs every deadline
change and failed read/write on the vsock connections.

Inside a VM the agent runs from `/var/run/lxd_agent` and logs to
`/var/log/lxd-agent.log`; `service lxd_agent restart` restarts it.
