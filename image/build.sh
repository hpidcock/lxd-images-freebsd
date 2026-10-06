#!/bin/sh
# build.sh turns one of the official FreeBSD virtual machine disk images
# published at https://download.freebsd.org/releases/VM-IMAGES/ into an LXD
# virtual-machine image in LXD's split format:
#
#   <name>.lxd.tar.xz    metadata tarball (metadata.yaml)
#   <name>.disk.qcow2    root disk, as a compressed qcow2
#   <name>.json          manifest (sizes, hashes and the combined hash LXD uses
#                        as the image fingerprint) consumed by
#                        .github/scripts/update_streams.py
#
# where <name> is freebsd-<release>-<arch>-<variant>.
#
# The source image is verified against FreeBSD's CHECKSUM.SHA256 before use.
# Nothing inside the image is modified: the UFS root filesystem cannot be
# written from Linux, and the stock image already does what an LXD VM needs
# (UEFI boot, virtio disk/network drivers in GENERIC, DHCP on the first NIC,
# growfs of the root filesystem on first boot).
#
# Usage:
#   image/build.sh -r RELEASE [-a ARCH] [-v VARIANT] [-o OUTDIR] [-c CACHEDIR]
#
#   -r RELEASE   FreeBSD release, e.g. 14.5 or 15.1 (required)
#   -a ARCH      amd64 (default) or arm64
#   -v VARIANT   default (plain image) or cloud (BASIC-CLOUDINIT image, which
#                enables sshd and FreeBSD's nuageinit cloud-init)
#   -o OUTDIR    output directory (default: image/dist)
#   -c CACHEDIR  where downloaded source images are kept (default: image/cache)
#
# Import the result with:
#   lxc image import image/dist/<name>.lxd.tar.xz image/dist/<name>.disk.qcow2 --alias freebsd
#   lxc launch freebsd fbsd --vm -c boot.mode=uefi-nosecureboot

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)

RELEASE=""
ARCH=amd64
VARIANT=default
OUTDIR="$SCRIPT_DIR/dist"
CACHEDIR="$SCRIPT_DIR/cache"
MIRROR=${FREEBSD_MIRROR:-https://download.freebsd.org/releases/VM-IMAGES}

while getopts "r:a:v:o:c:h" opt; do
	case "$opt" in
	r) RELEASE=$OPTARG ;;
	a) ARCH=$OPTARG ;;
	v) VARIANT=$OPTARG ;;
	o) OUTDIR=$OPTARG ;;
	c) CACHEDIR=$OPTARG ;;
	h)
		sed -n '2,32p' "$0"
		exit 0
		;;
	*)
		exit 1
		;;
	esac
done

if [ -z "$RELEASE" ]; then
	echo "build.sh: -r RELEASE is required (e.g. -r 15.1)" >&2
	exit 1
fi

# Simplestreams/LXD use amd64/arm64; FreeBSD names its download directories and
# files differently; metadata.yaml wants the uname -m style name.
case "$ARCH" in
amd64)
	FBSD_DIR=amd64
	FBSD_ARCH=amd64
	LXD_ARCH=x86_64
	;;
arm64)
	FBSD_DIR=aarch64
	FBSD_ARCH=arm64-aarch64
	LXD_ARCH=aarch64
	;;
*)
	echo "build.sh: unsupported ARCH '$ARCH' (want amd64 or arm64)" >&2
	exit 1
	;;
esac

case "$VARIANT" in
default) FBSD_FLAVOUR="" ;;
cloud) FBSD_FLAVOUR="-BASIC-CLOUDINIT" ;;
*)
	echo "build.sh: unsupported VARIANT '$VARIANT' (want default or cloud)" >&2
	exit 1
	;;
esac

for tool in curl xz qemu-img sha256sum tar; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "build.sh: missing required tool '$tool'" >&2
		exit 1
	fi
done

NAME="freebsd-$RELEASE-$ARCH-$VARIANT"
SRC_FILE="FreeBSD-$RELEASE-RELEASE-$FBSD_ARCH$FBSD_FLAVOUR-ufs.qcow2.xz"
SRC_URL="$MIRROR/$RELEASE-RELEASE/$FBSD_DIR/Latest/$SRC_FILE"
SUMS_URL="$MIRROR/$RELEASE-RELEASE/$FBSD_DIR/Latest/CHECKSUM.SHA256"

mkdir -p "$OUTDIR" "$CACHEDIR"
OUTDIR=$(CDPATH= cd -- "$OUTDIR" && pwd)
CACHEDIR=$(CDPATH= cd -- "$CACHEDIR" && pwd)

WORK_DIR=$(mktemp -d)
trap 'rm -rf -- "$WORK_DIR"' EXIT

echo "fetching checksums: $SUMS_URL" >&2
curl -fsSL --retry 5 --retry-delay 10 -o "$WORK_DIR/CHECKSUM.SHA256" "$SUMS_URL"
SRC_SHA256=$(awk -v f="($SRC_FILE)" '$2 == f { print $NF }' "$WORK_DIR/CHECKSUM.SHA256")
if [ -z "$SRC_SHA256" ]; then
	echo "build.sh: $SRC_FILE is not listed in $SUMS_URL" >&2
	exit 1
fi

verify_src() {
	[ -f "$CACHEDIR/$SRC_FILE" ] || return 1
	actual=$(sha256sum "$CACHEDIR/$SRC_FILE" | awk '{ print $1 }')
	[ "$actual" = "$SRC_SHA256" ]
}

if verify_src; then
	echo "using cached $CACHEDIR/$SRC_FILE" >&2
else
	echo "downloading $SRC_URL" >&2
	curl -fL --retry 5 --retry-delay 10 -o "$CACHEDIR/$SRC_FILE.part" "$SRC_URL"
	mv -- "$CACHEDIR/$SRC_FILE.part" "$CACHEDIR/$SRC_FILE"
	if ! verify_src; then
		echo "build.sh: sha256 mismatch for $SRC_FILE (expected $SRC_SHA256)" >&2
		exit 1
	fi
fi

echo "decompressing $SRC_FILE" >&2
xz -dc -- "$CACHEDIR/$SRC_FILE" >"$WORK_DIR/src.qcow2"

# LXD downloads disk-kvm.img as-is and converts it with qemu-img itself, so a
# qcow2 with compressed clusters is both accepted and a lot smaller to host.
echo "converting to compressed qcow2" >&2
qemu-img convert -f qcow2 -O qcow2 -c "$WORK_DIR/src.qcow2" "$OUTDIR/$NAME.disk.qcow2"
rm -f -- "$WORK_DIR/src.qcow2"

# FreeBSD's loader is not signed for UEFI Secure Boot, so flag the image as
# requiring it off; LXD then refuses to boot it with Secure Boot on and tells
# the user to set boot.mode=uefi-nosecureboot instead of silently hanging.
cat >"$WORK_DIR/metadata.yaml" <<YAML
architecture: $LXD_ARCH
creation_date: $(date +%s)
properties:
  os: FreeBSD
  release: "$RELEASE"
  variant: $VARIANT
  architecture: $ARCH
  description: FreeBSD $RELEASE-RELEASE $ARCH ($VARIANT)
  source: $SRC_URL
  requirements.secureboot: "false"
YAML

echo "packaging $NAME.lxd.tar.xz" >&2
tar --numeric-owner --owner=0 --group=0 -C "$WORK_DIR" -cJf "$OUTDIR/$NAME.lxd.tar.xz" metadata.yaml

META_SIZE=$(stat -c %s "$OUTDIR/$NAME.lxd.tar.xz")
META_SHA256=$(sha256sum "$OUTDIR/$NAME.lxd.tar.xz" | awk '{ print $1 }')
DISK_SIZE=$(stat -c %s "$OUTDIR/$NAME.disk.qcow2")
DISK_SHA256=$(sha256sum "$OUTDIR/$NAME.disk.qcow2" | awk '{ print $1 }')
# LXD's fingerprint for a split VM image is the sha256 of the metadata tarball
# followed by the disk image.
COMBINED_SHA256=$(cat "$OUTDIR/$NAME.lxd.tar.xz" "$OUTDIR/$NAME.disk.qcow2" | sha256sum | awk '{ print $1 }')

cat >"$OUTDIR/$NAME.json" <<JSON
{
  "os": "FreeBSD",
  "release": "$RELEASE",
  "arch": "$ARCH",
  "variant": "$VARIANT",
  "source": "$SRC_URL",
  "source_sha256": "$SRC_SHA256",
  "fingerprint": "$COMBINED_SHA256",
  "items": {
    "lxd.tar.xz": {
      "ftype": "lxd.tar.xz",
      "file": "$NAME.lxd.tar.xz",
      "size": $META_SIZE,
      "sha256": "$META_SHA256",
      "combined_disk-kvm-img_sha256": "$COMBINED_SHA256"
    },
    "disk-kvm.img": {
      "ftype": "disk-kvm.img",
      "file": "$NAME.disk.qcow2",
      "size": $DISK_SIZE,
      "sha256": "$DISK_SHA256"
    }
  }
}
JSON

echo "done: $OUTDIR/$NAME.lxd.tar.xz $OUTDIR/$NAME.disk.qcow2 (fingerprint $COMBINED_SHA256)" >&2
