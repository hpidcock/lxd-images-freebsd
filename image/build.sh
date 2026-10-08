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
# The UFS root filesystem cannot be written from Linux, so the image is
# customised by a throwaway FreeBSD VM (the BASIC-CLOUDINIT image of the same
# release and architecture) booted with QEMU: it runs image/customize.sh, which
# builds the vsock kernel modules (kmod/), installs the LXD agent (agent/) and
# the rc scripts (image/overlay/) into the target disk, and powers off.
#
# Usage:
#   image/build.sh -r RELEASE [-a ARCH] [-v VARIANT] [-o OUTDIR] [-c CACHEDIR] [-A AGENT] [-n]
#
#   -r RELEASE   FreeBSD release, e.g. 14.5 or 15.1 (required)
#   -a ARCH      amd64 (default) or arm64
#   -v VARIANT   default (plain image) or cloud (BASIC-CLOUDINIT image, which
#                enables sshd and FreeBSD's nuageinit cloud-init)
#   -o OUTDIR    output directory (default: image/dist)
#   -c CACHEDIR  where downloaded source images are kept (default: image/cache)
#   -A AGENT     prebuilt lxd-agent binary for ARCH (default: build it with go)
#   -n           do not customise the image (plain repackaging of the FreeBSD image)
#
# Needs curl, xz, qemu-img, sha256sum, tar, and for the customisation step
# qemu-system-x86_64 / qemu-system-aarch64, genisoimage and go (or -A). KVM is
# used when /dev/kvm is usable, otherwise QEMU falls back to emulation. The
# aarch64 builder needs UEFI firmware: set QEMU_EFI_AARCH64 to a QEMU_EFI.fd
# (the nix dev shell does) or install qemu-efi-aarch64.
#
# Import the result with:
#   lxc image import image/dist/<name>.lxd.tar.xz image/dist/<name>.disk.qcow2 --alias freebsd
#   lxc launch freebsd fbsd --vm -c boot.mode=uefi-nosecureboot

set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
REPO_DIR=$(CDPATH= cd -- "$SCRIPT_DIR/.." && pwd)

RELEASE=""
ARCH=amd64
VARIANT=default
OUTDIR="$SCRIPT_DIR/dist"
CACHEDIR="$SCRIPT_DIR/cache"
AGENT=""
CUSTOMIZE=1
MIRROR=${FREEBSD_MIRROR:-https://download.freebsd.org/releases}
BUILDER_MEMORY=${BUILDER_MEMORY:-4096}
BUILDER_TIMEOUT=${BUILDER_TIMEOUT:-5400}

while getopts "r:a:v:o:c:A:nh" opt; do
	case "$opt" in
	r) RELEASE=$OPTARG ;;
	a) ARCH=$OPTARG ;;
	v) VARIANT=$OPTARG ;;
	o) OUTDIR=$OPTARG ;;
	c) CACHEDIR=$OPTARG ;;
	A) AGENT=$OPTARG ;;
	n) CUSTOMIZE=0 ;;
	h)
		sed -n '2,42p' "$0"
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
	FBSD_DIST_DIR=amd64
	LXD_ARCH=x86_64
	GOARCH=amd64
	QEMU=qemu-system-x86_64
	;;
arm64)
	FBSD_DIR=aarch64
	FBSD_ARCH=arm64-aarch64
	FBSD_DIST_DIR=arm64/aarch64
	LXD_ARCH=aarch64
	GOARCH=arm64
	QEMU=qemu-system-aarch64
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

TOOLS="curl xz qemu-img sha256sum tar"
if [ "$CUSTOMIZE" = 1 ]; then
	TOOLS="$TOOLS $QEMU genisoimage"
	[ -n "$AGENT" ] || TOOLS="$TOOLS go"
fi
for tool in $TOOLS; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "build.sh: missing required tool '$tool'" >&2
		exit 1
	fi
done

NAME="freebsd-$RELEASE-$ARCH-$VARIANT"
VM_URL="$MIRROR/VM-IMAGES/$RELEASE-RELEASE/$FBSD_DIR/Latest"
SRC_FILE="FreeBSD-$RELEASE-RELEASE-$FBSD_ARCH$FBSD_FLAVOUR-ufs.qcow2.xz"
SRC_URL="$VM_URL/$SRC_FILE"
BUILDER_FILE="FreeBSD-$RELEASE-RELEASE-$FBSD_ARCH-BASIC-CLOUDINIT-ufs.qcow2.xz"
DIST_URL="$MIRROR/$FBSD_DIST_DIR/$RELEASE-RELEASE"
SRCTXZ_FILE="FreeBSD-$RELEASE-RELEASE-$FBSD_ARCH-src.txz"

mkdir -p "$OUTDIR" "$CACHEDIR"
OUTDIR=$(CDPATH= cd -- "$OUTDIR" && pwd)
CACHEDIR=$(CDPATH= cd -- "$CACHEDIR" && pwd)

WORK_DIR=$(mktemp -d)
trap 'rm -rf -- "$WORK_DIR"' EXIT

# fetch_verified URL FILE SHA256: download URL into the cache as FILE unless a
# copy with the expected sha256 is already there.
fetch_verified() {
	url=$1
	file=$2
	expected=$3

	if [ -f "$CACHEDIR/$file" ]; then
		actual=$(sha256sum "$CACHEDIR/$file" | awk '{ print $1 }')
		if [ "$actual" = "$expected" ]; then
			echo "using cached $CACHEDIR/$file" >&2
			return 0
		fi
		echo "cached $file has a different checksum, re-downloading" >&2
	fi

	echo "downloading $url" >&2
	curl -fL --retry 5 --retry-delay 10 -o "$CACHEDIR/$file.part" "$url"
	actual=$(sha256sum "$CACHEDIR/$file.part" | awk '{ print $1 }')
	if [ "$actual" != "$expected" ]; then
		rm -f -- "$CACHEDIR/$file.part"
		echo "build.sh: sha256 mismatch for $file (expected $expected, got $actual)" >&2
		exit 1
	fi
	mv -- "$CACHEDIR/$file.part" "$CACHEDIR/$file"
}

echo "fetching checksums: $VM_URL/CHECKSUM.SHA256" >&2
curl -fsSL --retry 5 --retry-delay 10 -o "$WORK_DIR/CHECKSUM.SHA256" "$VM_URL/CHECKSUM.SHA256"
vm_sha256() {
	awk -v f="($1)" '$2 == f { print $NF }' "$WORK_DIR/CHECKSUM.SHA256"
}

SRC_SHA256=$(vm_sha256 "$SRC_FILE")
if [ -z "$SRC_SHA256" ]; then
	echo "build.sh: $SRC_FILE is not listed in $VM_URL/CHECKSUM.SHA256" >&2
	exit 1
fi
fetch_verified "$SRC_URL" "$SRC_FILE" "$SRC_SHA256"

echo "decompressing $SRC_FILE" >&2
xz -dc -- "$CACHEDIR/$SRC_FILE" >"$WORK_DIR/target.qcow2"

# ---------------------------------------------------------------------------
# Customisation in a FreeBSD builder VM.
# ---------------------------------------------------------------------------
customize() {
	# Builder: the BASIC-CLOUDINIT image, whose nuageinit runs our user-data script.
	BUILDER_SHA256=$(vm_sha256 "$BUILDER_FILE")
	if [ -z "$BUILDER_SHA256" ]; then
		echo "build.sh: $BUILDER_FILE is not listed in $VM_URL/CHECKSUM.SHA256" >&2
		exit 1
	fi
	fetch_verified "$VM_URL/$BUILDER_FILE" "$BUILDER_FILE" "$BUILDER_SHA256"

	# Kernel sources of the target release, for building the modules.
	echo "fetching distribution manifest: $DIST_URL/MANIFEST" >&2
	curl -fsSL --retry 5 --retry-delay 10 -o "$WORK_DIR/MANIFEST" "$DIST_URL/MANIFEST"
	SRCTXZ_SHA256=$(awk -F'\t' '$1 == "src.txz" { print $2 }' "$WORK_DIR/MANIFEST")
	if [ -z "$SRCTXZ_SHA256" ]; then
		echo "build.sh: src.txz is not listed in $DIST_URL/MANIFEST" >&2
		exit 1
	fi
	fetch_verified "$DIST_URL/src.txz" "$SRCTXZ_FILE" "$SRCTXZ_SHA256"

	# The agent.
	if [ -z "$AGENT" ]; then
		echo "building lxd-agent for freebsd/$GOARCH" >&2
		(cd "$REPO_DIR/agent" && GOOS=freebsd GOARCH=$GOARCH CGO_ENABLED=0 go build -tags agent,netgo -trimpath -ldflags "-s -w" -o "$WORK_DIR/lxd-agent" .)
		AGENT="$WORK_DIR/lxd-agent"
	fi

	# Seed ISO: NoCloud seed for the builder's nuageinit plus our payload.
	echo "preparing seed ISO" >&2
	SEED="$WORK_DIR/seed"
	mkdir -p "$SEED/payload"
	printf 'instance-id: lxd-image-builder\nlocal-hostname: builder\n' >"$SEED/meta-data"
	cp "$SCRIPT_DIR/customize.sh" "$SEED/customize.sh"
	# The cloud-config disables the BASIC-CLOUDINIT image's first-boot "pkg
	# upgrade" (hundreds of MB we do not need) before the network comes up, and
	# runs customize.sh from the seed through nuageinit's runcmd late in the boot.
	cat >"$SEED/user-data" <<'USERDATA'
#cloud-config
users: []
write_files:
  - path: /etc/rc.conf.d/firstboot_pkg_upgrade
    permissions: "0644"
    content: |
      firstboot_pkg_upgrade_enable="NO"
runcmd:
  - mkdir -p /mnt/seed
  - mount -t cd9660 /dev/iso9660/[cC][iI][dD][aA][tT][aA] /mnt/seed
  - sh /mnt/seed/customize.sh
USERDATA
	cp "$AGENT" "$SEED/payload/lxd-agent"
	cp -R "$REPO_DIR/kmod" "$SEED/payload/kmod"
	cp -R "$SCRIPT_DIR/overlay" "$SEED/payload/overlay"
	ln -s "$CACHEDIR/$SRCTXZ_FILE" "$SEED/payload/src.txz"
	git_rev=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo unknown)
	lxd_rev=$(sed -n 's|^\tgithub.com/canonical/lxd \(.*\)$|\1|p' "$REPO_DIR/agent/go.mod")
	cat >"$SEED/payload/build-info" <<INFO
image: $NAME
built: $(date -u +%Y-%m-%dT%H:%M:%SZ)
source: $SRC_URL
lxd-images-freebsd: $git_rev
lxd-agent based on: github.com/canonical/lxd $lxd_rev
INFO
	genisoimage -quiet -V cidata -R -J -f -o "$WORK_DIR/seed.iso" "$SEED"

	# Builder disk: a throwaway overlay on top of the decompressed builder image.
	echo "decompressing $BUILDER_FILE" >&2
	xz -dc -- "$CACHEDIR/$BUILDER_FILE" >"$WORK_DIR/builder.qcow2"
	qemu-img create -q -f qcow2 -b "$WORK_DIR/builder.qcow2" -F qcow2 "$WORK_DIR/builder-overlay.qcow2"

	# QEMU command line.
	if [ -r /dev/kvm ] && [ -w /dev/kvm ] && [ "$(uname -m)" = "$LXD_ARCH" ]; then
		accel="kvm"
		cpu="host"
	else
		accel="tcg"
		cpu="max"
		echo "note: KVM not available for $LXD_ARCH, QEMU will emulate the builder (slow)" >&2
	fi
	smp=$(nproc 2>/dev/null || echo 2)
	[ "$smp" -gt 8 ] && smp=8

	set -- -accel "$accel" -cpu "$cpu" -smp "$smp" -m "$BUILDER_MEMORY" \
		-display none -monitor none -no-reboot \
		-serial "file:$WORK_DIR/console.log" \
		-drive "file=$WORK_DIR/builder-overlay.qcow2,if=virtio,format=qcow2,cache=unsafe" \
		-drive "file=$WORK_DIR/target.qcow2,if=virtio,format=qcow2" \
		-device virtio-scsi-pci,id=scsi0 \
		-drive "file=$WORK_DIR/seed.iso,if=none,format=raw,media=cdrom,readonly=on,id=seed" \
		-device scsi-cd,drive=seed,bus=scsi0.0 \
		-netdev user,id=net0 -device virtio-net-pci,netdev=net0 \
		-device virtio-rng-pci

	case "$ARCH" in
	amd64)
		# The FreeBSD images carry a freebsd-boot partition, so BIOS boot works.
		set -- -machine q35 "$@"
		;;
	arm64)
		firmware=${QEMU_EFI_AARCH64:-}
		for f in /usr/share/qemu-efi-aarch64/QEMU_EFI.fd /usr/share/AAVMF/AAVMF_CODE.fd /usr/share/edk2/aarch64/QEMU_EFI.fd; do
			[ -n "$firmware" ] || [ ! -f "$f" ] || firmware=$f
		done
		if [ -z "$firmware" ]; then
			echo "build.sh: no aarch64 UEFI firmware found (set QEMU_EFI_AARCH64)" >&2
			exit 1
		fi
		[ "$cpu" = "max" ] && cpu="cortex-a72"
		set -- -machine virt -bios "$firmware" "$@"
		;;
	esac

	echo "booting FreeBSD $RELEASE $ARCH builder VM ($accel, $smp CPUs, ${BUILDER_MEMORY}M), console in $WORK_DIR/console.log" >&2
	start=$(date +%s)
	# Stream the console to stderr so CI logs show progress.
	: >"$WORK_DIR/console.log"
	tail -f -n +1 "$WORK_DIR/console.log" | sed -u 's/^/  vm: /' >&2 &
	tail_pid=$!
	status=0
	timeout "$BUILDER_TIMEOUT" "$QEMU" "$@" || status=$?
	sleep 1
	kill "$tail_pid" 2>/dev/null || :
	echo "builder VM finished in $(( $(date +%s) - start ))s (qemu exit status $status)" >&2

	if [ "$status" -ne 0 ] || ! grep -q 'LXD-IMAGE-CUSTOMIZE: OK' "$WORK_DIR/console.log"; then
		echo "build.sh: customisation failed; last console lines:" >&2
		tail -n 40 "$WORK_DIR/console.log" >&2
		exit 1
	fi
}

if [ "$CUSTOMIZE" = 1 ]; then
	customize
	IMAGE_FLAVOUR="with lxd-agent"
else
	IMAGE_FLAVOUR="unmodified"
fi

# LXD downloads disk-kvm.img as-is and converts it with qemu-img itself, so a
# qcow2 with compressed clusters is both accepted and a lot smaller to host.
echo "converting to compressed qcow2" >&2
qemu-img convert -f qcow2 -O qcow2 -c "$WORK_DIR/target.qcow2" "$OUTDIR/$NAME.disk.qcow2"
rm -f -- "$WORK_DIR/target.qcow2"

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
  description: FreeBSD $RELEASE-RELEASE $ARCH ($VARIANT, $IMAGE_FLAVOUR)
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
