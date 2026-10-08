#!/bin/sh
# customize.sh runs inside the throwaway FreeBSD builder VM that image/build.sh
# boots with QEMU. The builder's NoCloud user-data (a cloud-config written by
# build.sh) runs it through nuageinit(7)'s runcmd late in the first boot. It
# mounts the target image (the second virtio disk), builds the vsock kernel
# modules for the target release, installs the LXD agent and its rc scripts,
# and powers the VM off. The host watches the serial console for the final
# "LXD-IMAGE-CUSTOMIZE: OK" marker.
#
# Inputs come from the "cidata" ISO (also the NoCloud seed), under payload/:
#   lxd-agent    the agent binary for the target architecture
#   src.txz      the target release's source distribution (for kernel headers)
#   kmod/        module sources and kmod/build.sh
#   overlay/     files copied verbatim onto the target root filesystem
#   build-info   provenance text installed as /usr/local/share/lxd-images-freebsd/build-info

set -eu

MARK_OK="LXD-IMAGE-CUSTOMIZE: OK"
MARK_FAIL="LXD-IMAGE-CUSTOMIZE: FAILED"
SEED=/mnt/seed
TARGET=/mnt/target
TARGET_DISK=${TARGET_DISK:-vtbd1}

log() {
	echo "customize: $*"
}

finish() {
	status=$?
	set +e
	if [ "$status" -eq 0 ]; then
		echo "$MARK_OK"
	else
		echo "$MARK_FAIL (exit status $status)"
	fi
	umount "$TARGET" 2>/dev/null
	umount "$SEED" 2>/dev/null
	sync
	sleep 2
	poweroff
}

trap finish EXIT

log "FreeBSD $(uname -r) $(uname -m), $(sysctl -n hw.ncpu) CPUs, $(($(sysctl -n hw.physmem) / 1048576)) MB"

# Find and mount the seed ISO unless the runcmd that started us already did.
if [ ! -d "$SEED/payload" ]; then
	seed_dev=""
	for d in /dev/iso9660/[cC][iI][dD][aA][tT][aA]; do
		[ -e "$d" ] && seed_dev=$d
	done
	if [ -z "$seed_dev" ]; then
		log "no cidata ISO found"
		exit 1
	fi
	mkdir -p "$SEED"
	mount -t cd9660 "$seed_dev" "$SEED"
	log "seed mounted from $seed_dev"
fi
PAYLOAD="$SEED/payload"

# Find and mount the root filesystem of the target disk.
if [ ! -e "/dev/$TARGET_DISK" ]; then
	log "target disk /dev/$TARGET_DISK not found"
	exit 1
fi
root_part=$(gpart show -p "$TARGET_DISK" | awk '$4 == "freebsd-ufs" { print $3; exit }')
if [ -z "$root_part" ]; then
	log "no freebsd-ufs partition on $TARGET_DISK"
	gpart show -p "$TARGET_DISK"
	exit 1
fi
log "target root filesystem is /dev/$root_part"
fsck_ufs -p "/dev/$root_part" || :
mkdir -p "$TARGET"
mount -o noatime "/dev/$root_part" "$TARGET"

# Build the vsock kernel modules against the target release's kernel sources.
log "building vsock kernel modules"
sh "$PAYLOAD/kmod/build.sh" -s "$PAYLOAD/src.txz" -o /tmp/kmod
install -d -m 755 "$TARGET/boot/modules"
install -m 555 /tmp/kmod/vsock.ko /tmp/kmod/virtio_socket.ko "$TARGET/boot/modules/"
kldxref "$TARGET/boot/modules"

# Install the agent.
log "installing lxd-agent"
install -d -m 755 "$TARGET/usr/local/bin" "$TARGET/usr/local/etc/rc.d" "$TARGET/usr/local/share/lxd-images-freebsd"
install -m 755 "$PAYLOAD/lxd-agent" "$TARGET/usr/local/bin/lxd-agent"
install -m 644 "$PAYLOAD/build-info" "$TARGET/usr/local/share/lxd-images-freebsd/build-info"

# Install the overlay (rc scripts, rc.conf.d).
log "installing overlay"
(cd "$PAYLOAD/overlay" && find . -type d) | while read -r d; do
	install -d -m 755 "$TARGET/$d"
done
(cd "$PAYLOAD/overlay" && find . -type f) | while read -r f; do
	case "$f" in
	*/rc.d/*) mode=755 ;;
	*) mode=644 ;;
	esac
	install -m "$mode" "$PAYLOAD/overlay/$f" "$TARGET/$f"
done

# The agent is useless without the modules it needs: fail loudly if rc would not find them.
for f in boot/modules/vsock.ko boot/modules/virtio_socket.ko usr/local/bin/lxd-agent usr/local/etc/rc.d/lxd_agent etc/rc.conf.d/lxd_agent; do
	if [ ! -e "$TARGET/$f" ]; then
		log "missing $f in target"
		exit 1
	fi
done

log "target disk usage: $(df -h "$TARGET" | awk 'NR == 2 { print $3 " used, " $4 " free" }')"
umount "$TARGET"
log "done"
