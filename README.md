# lxd-images-freebsd

FreeBSD virtual-machine images for [LXD](https://canonical.com/lxd), built from
the official FreeBSD VM disk images at
<https://download.freebsd.org/releases/VM-IMAGES/>, with a FreeBSD port of the
LXD agent installed, and served from GitHub releases as a
[simplestreams](https://documentation.ubuntu.com/lxd/latest/reference/remote_image_servers/)
image remote.

## Using the images

```sh
lxc remote add freebsd https://github.com/hpidcock/lxd-images-freebsd/releases/download --protocol simplestreams
lxc image list freebsd:
lxc launch freebsd:15.1 fbsd --vm -c boot.mode=uefi-nosecureboot
lxc console fbsd
```

FreeBSD's boot loader is not signed for UEFI Secure Boot, so the images carry
the `requirements.secureboot=false` property and LXD refuses to start them
unless Secure Boot is turned off. On LXD 6 use `boot.mode=uefi-nosecureboot`;
on LXD 5.21 use `security.secureboot=false`.

The images contain a FreeBSD build of the LXD agent (see [`agent/`](agent/)),
so `lxc exec`, `lxc file`, `lxc console`, `lxc list`/`lxc info` (addresses,
state, metrics) and the `/1.0` devlxd API (at `/var/run/lxd/sock`, not
`/dev/lxd/sock`, because FreeBSD's devfs cannot hold directories) work as they
do with Linux VMs. `lxc console` is attached to the serial console: log in as
`root` with no password. The root filesystem grows to the size of the root disk
on first boot.

LXD talks to the agent over a virtio socket (`AF_VSOCK`), which FreeBSD's
kernel does not support yet. The images ship it as two kernel modules built
from Danilo Egea Gondolfo's in-progress driver (see [`kmod/`](kmod/)); the
`lxd_agent` rc script loads them, mounts LXD's 9p config share and starts the
agent, which logs to `/var/log/lxd-agent.log`. Everything is skipped when the
image is not booted as a LXD VM.

Known gaps: disk I/O metrics are not reported, and hot-plugging `disk`
devices with a `path` (which LXD shares over virtiofs) does not work because
FreeBSD has no virtiofs client; attach such disks before starting the VM so
they are mounted with 9p instead.

### cloud-init

FreeBSD has no cloud-init in base, but it has
[nuageinit(7)](https://man.freebsd.org/cgi/man.cgi?query=nuageinit&sektion=7),
a Lua implementation of the parts of cloud-init a cloud image needs. The
`lxd_cloudinit` rc scripts feed it what LXD provides, so the usual LXD
cloud-init configuration keys work for the things nuageinit supports:

```sh
lxc launch freebsd:15.1 fbsd --vm -c boot.mode=uefi-nosecureboot \
  -c cloud-init.user-data="$(cat <<'CLOUDCFG'
#cloud-config
users:
  - name: demo
    groups: wheel
    ssh_authorized_keys:
      - ssh-ed25519 AAAA... you@example
runcmd:
  - sysrc sshd_enable=YES
  - service sshd start
CLOUDCFG
)"
```

What happens on boot: `lxd-agent cloud-init` fetches `meta-data`,
`cloud-init.user-data` (or `user.user-data`), `cloud-init.vendor-data` and
`cloud-init.network-config` from the devlxd API, writes a NoCloud seed under
`/var/cache/lxd-cloudinit/seed` and runs `nuageinit <seed> nocloud` before the
network comes up; after the network is up the `packages`, `users`, `chpasswd`
and deferred `write_files` stages run, and `runcmd` and `#!` user-data scripts
run at the end of the boot. The hostname is always set to the instance name
from `meta-data`. It runs once per cloud-init instance ID (LXD changes it on
rename and with `lxc config set ... volatile.cloud-init.instance-id`), recorded
in `/var/db/lxd-agent/cloud-init.instance-id`; the log is
`/var/log/lxd-cloudinit.log`.

Limitations, all inherited from nuageinit: only `#cloud-config` and `#!`
script user-data are understood; vendor-data is used only when there is no
user-data (nuageinit cannot merge the two); `network-config` must be version 2
(netplan style); unsupported `#cloud-config` keys are silently ignored. If
user-data is given without a `users` list nuageinit creates its default
`freebsd` user (password `freebsd`), as it does on other clouds; when no
user-data or vendor-data is set at all, only the hostname is configured. The
`cloud` variant's own nuageinit, which reads a `cidata`/`config-2` disk, keeps
working and takes over the post-network stages when it is active.

Images are published per FreeBSD release, architecture and variant:

| Alias                        | Source image                              |
| ---------------------------- | ----------------------------------------- |
| `freebsd:<release>`          | `FreeBSD-<release>-RELEASE-<arch>-ufs`     |
| `freebsd:<release>/cloud`    | `...-BASIC-CLOUDINIT-ufs`, with `sshd` enabled and FreeBSD's nuageinit enabled for `cidata`/`config-2` seed disks |

Both variants use the UFS root filesystem, DHCP (plus IPv6 router
advertisements) on the first NIC, and contain the LXD agent and the cloud-init
glue described above. The `cloud` variant's `sshd` has no keys and refuses
password logins for `root` out of the box; use cloud-init to add a user or keys.

Releases and architectures are listed in the build matrix of
[`.github/workflows/release.yml`](.github/workflows/release.yml): currently
FreeBSD 14.4, 14.5, 15.0 and 15.1 on `amd64` and `arm64`.

## Building locally

```sh
nix develop            # or install the tools listed below
image/build.sh -r 15.1 -a amd64 -v default
lxc image import image/dist/freebsd-15.1-amd64-default.lxd.tar.xz \
  image/dist/freebsd-15.1-amd64-default.disk.qcow2 --alias freebsd/15.1
```

`image/build.sh` needs `curl`, `xz`, `qemu-img`, `tar`, `genisoimage`,
`qemu-system-x86_64` (or `qemu-system-aarch64` plus UEFI firmware for arm64;
the nix shell provides both) and Go, or a prebuilt agent passed with `-A`.
The flake's dev shell has everything.

It downloads the source VM image (cached under `image/cache/`), verifies it
against FreeBSD's `CHECKSUM.SHA256`, and then, because a UFS root filesystem
cannot be written from Linux, customises it from a throwaway FreeBSD VM: it
boots the release's BASIC-CLOUDINIT image under QEMU (with KVM when available)
with the target disk attached as a second disk and a `cidata` seed ISO
carrying `image/customize.sh`, the agent, the module sources and the release's
`src.txz`. Inside the VM, `customize.sh` builds the vsock modules against the
release's kernel sources (`kmod/build.sh`), installs them, the agent and the
rc scripts from `image/overlay/` into the target disk, and powers off. The
result is recompressed as a qcow2 with compressed clusters and packaged in
LXD's split VM image format plus a JSON manifest under `image/dist/`:

- `<name>.lxd.tar.xz` – metadata tarball (`metadata.yaml`)
- `<name>.disk.qcow2` – root disk
- `<name>.json` – sizes, hashes and the combined hash LXD uses as the image
  fingerprint, consumed by `.github/scripts/update_streams.py`

`image/build.sh -n` skips the customisation and repackages the stock image.

### The agent

[`agent/`](agent/) is a copy of LXD's `lxd-agent` (from the LXD commit pinned
in `agent/go.mod`, which it also depends on for the packages that build on
FreeBSD) with FreeBSD replacements for the Linux-only parts: `AF_VSOCK`
sockets, pseudo-terminals for `lxc exec`, mounting of 9p shares with p9fs,
interface renaming/MTU with `ifconfig`, and state and metrics from `sysctl`
(disk I/O metrics are not implemented). It adds the `lxd-agent cloud-init`
subcommand. Build and test it with:

```sh
cd agent
GOOS=freebsd GOARCH=amd64 CGO_ENABLED=0 go build -tags agent,netgo -o lxd-agent .
go test ./...
```

Inside a VM the agent runs from `/var/run/lxd_agent` (a tmpfs with a copy of
LXD's config share) and logs to `/var/log/lxd-agent.log`.

## Releasing

Pushing a tag such as `v1.0.0`, or running the *Release* workflow manually with
a version, does the following:

1. creates the GitHub release for the tag if it does not exist;
2. builds every image in the matrix and uploads the `.lxd.tar.xz` and
   `.disk.qcow2` files as assets of that release;
3. regenerates `streams/v1/index.json` and `streams/v1/images.json` from the
   previous metadata plus the new manifests and uploads them to the
   `streams/v1` release, which is the simplestreams index LXD reads.

Each build adds a version to the per-release/arch/variant product, so older
versions stay available at their own release tags and LXD picks the newest.
