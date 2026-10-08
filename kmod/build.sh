#!/bin/sh
# build.sh builds the vsock(4)/virtio_socket(4) kernel modules for a FreeBSD
# release from that release's src.txz. It must run on FreeBSD (the image build
# runs it inside the throwaway builder VM) with the base system toolchain.
#
# The module sources under kmod/sys/ come from Danilo Egea Gondolfo's
# virtio_vsocks branch (see kmod/README.md). They need two header additions
# that the branch makes to sys/sys/socket.h and sys/sys/sockio.h; rather than
# patching those files, which differ between releases, the definitions are
# appended here. AF_VSOCK must match agent/internal/vsock/vsock_freebsd.go.
#
# Usage: build.sh -s SRC_TXZ -o OUTDIR [-w WORKDIR]
#
#   -s SRC_TXZ  the release's src.txz (only usr/src/sys is extracted)
#   -o OUTDIR   where vsock.ko and virtio_socket.ko are written
#   -w WORKDIR  where the source tree is extracted (default: /usr/src, which is
#               what the module Makefiles' SRCTOP fallback expects)

set -eu

KMOD_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
SRC_TXZ=""
OUTDIR=""
WORKDIR=/usr/src
AF_VSOCK=48

while getopts "s:o:w:h" opt; do
	case "$opt" in
	s) SRC_TXZ=$OPTARG ;;
	o) OUTDIR=$OPTARG ;;
	w) WORKDIR=$OPTARG ;;
	h)
		sed -n '2,19p' "$0"
		exit 0
		;;
	*) exit 1 ;;
	esac
done

if [ -z "$SRC_TXZ" ] || [ -z "$OUTDIR" ]; then
	echo "build.sh: -s SRC_TXZ and -o OUTDIR are required" >&2
	exit 1
fi

for tool in cc make tar; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "build.sh: missing required tool '$tool'" >&2
		exit 1
	fi
done

SYSDIR="$WORKDIR/sys"

if [ ! -f "$SYSDIR/conf/kmod.mk" ]; then
	echo "extracting kernel sources from $SRC_TXZ to $WORKDIR" >&2
	mkdir -p "$WORKDIR"
	# src.txz contains usr/src/...; keep only the kernel sources.
	tar -xf "$SRC_TXZ" -C "$WORKDIR" --strip-components 2 usr/src/sys
	if [ ! -f "$SYSDIR/conf/kmod.mk" ]; then
		echo "build.sh: $SRC_TXZ did not contain usr/src/sys" >&2
		exit 1
	fi
fi

echo "adding vsock sources to $SYSDIR" >&2
cp -R "$KMOD_DIR/sys/." "$SYSDIR/"

# Header additions from the virtio_vsocks branch.
if ! grep -q 'AF_VSOCK' "$SYSDIR/sys/socket.h"; then
	cat >>"$SYSDIR/sys/socket.h" <<HDR

/* Added by lxd-images-freebsd kmod/build.sh (virtio_vsocks branch). */
#define	AF_VSOCK	$AF_VSOCK		/* Virtio VSOCK */
#define	PF_VSOCK	AF_VSOCK
HDR
fi

if ! grep -q 'IOCTL_VM_SOCKETS_GET_LOCAL_CID' "$SYSDIR/sys/sockio.h"; then
	cat >>"$SYSDIR/sys/sockio.h" <<HDR

/* Added by lxd-images-freebsd kmod/build.sh (virtio_vsocks branch). */
#define	IOCTL_VM_SOCKETS_GET_LOCAL_CID	_IOR('s', 160, uint32_t)
HDR
fi

mkdir -p "$OUTDIR"
OUTDIR=$(CDPATH= cd -- "$OUTDIR" && pwd)

for module in vsock virtio/socket; do
	echo "building $module" >&2
	make -C "$SYSDIR/modules/$module" SRCTOP="$WORKDIR" SYSDIR="$SYSDIR" -j"$(sysctl -n hw.ncpu)" all
	make -C "$SYSDIR/modules/$module" SRCTOP="$WORKDIR" SYSDIR="$SYSDIR" KMODDIR="$OUTDIR" DESTDIR="" install
done

ls -l "$OUTDIR"/*.ko
