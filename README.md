# lxd-images-freebsd

FreeBSD virtual-machine images for [LXD](https://canonical.com/lxd), built from
the official FreeBSD VM disk images at
<https://download.freebsd.org/releases/VM-IMAGES/> and served from GitHub
releases as a [simplestreams](https://documentation.ubuntu.com/lxd/latest/reference/remote_image_servers/)
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

The images do not contain the LXD agent (it only exists for Linux guests), so
`lxc exec` and `lxc file` do not work; `lxc list` still shows the addresses the
VM obtained from LXD's DHCP. Use `lxc console` instead, which is attached to the
serial console: log in as `root` with no password. The root filesystem grows
to the size of the root disk on first boot.

Images are published per FreeBSD release, architecture and variant:

| Alias                        | Source image                              |
| ---------------------------- | ----------------------------------------- |
| `freebsd:<release>`          | `FreeBSD-<release>-RELEASE-<arch>-ufs`     |
| `freebsd:<release>/cloud`    | `...-BASIC-CLOUDINIT-ufs`, with `sshd` and FreeBSD's `nuageinit` cloud-init enabled |

Both variants use the UFS root filesystem and DHCP (plus IPv6 router
advertisements) on the first NIC.

The `cloud` variant's `nuageinit` only reads NoCloud/ConfigDrive seeds from a
disk labelled `cidata` or `config-2`, not LXD's `cloud-init.*` config, so
attach a seed ISO as an extra disk device if you want to use it (for example to
add SSH keys; `sshd` runs but has no keys and refuses password logins for
`root` out of the box).

Releases and architectures are listed in the build matrix of
[`.github/workflows/release.yml`](.github/workflows/release.yml): currently
FreeBSD 14.4, 14.5, 15.0 and 15.1 on `amd64` and `arm64`.

## Building locally

```sh
image/build.sh -r 15.1 -a amd64 -v default
lxc image import image/dist/freebsd-15.1-amd64-default.lxd.tar.xz \
  image/dist/freebsd-15.1-amd64-default.disk.qcow2 --alias freebsd/15.1
```

`image/build.sh` needs `curl`, `xz`, `qemu-img` and `tar`. It downloads the
source image (cached under `image/cache/`), verifies it against FreeBSD's
`CHECKSUM.SHA256`, recompresses the disk as a qcow2 with compressed clusters,
and writes LXD's split VM image format plus a JSON manifest to `image/dist/`:

- `<name>.lxd.tar.xz` – metadata tarball (`metadata.yaml`)
- `<name>.disk.qcow2` – root disk
- `<name>.json` – sizes, hashes and the combined hash LXD uses as the image
  fingerprint, consumed by `.github/scripts/update_streams.py`

The image contents are not modified: the UFS root filesystem cannot be written
from Linux, and the stock image already boots under LXD's UEFI firmware with
virtio disk and network devices.

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
