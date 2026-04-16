#!/bin/bash
#
# Build an Ubuntu 24.04 (Noble) rootfs as a squashfs layer.
#
# Requires debootstrap on the host:
#   apt-get install -y debootstrap
#

set -e

TMP="$(mktemp --directory)"

cleanup() {
	# Unmount bind mounts
	umount "${TMP}/root/dev/pts" 2>/dev/null || true
	umount "${TMP}/root/dev" 2>/dev/null || true
	umount "${TMP}/root/proc" 2>/dev/null || true
	umount "${TMP}/root/sys" 2>/dev/null || true
	# Clear resolv.conf
	>"${TMP}/root/etc/resolv.conf" 2>/dev/null || true
	rm -rf "${TMP}"
}
trap cleanup EXIT

mkdir -p "${TMP}/root"

# Build rootfs from cloud image (fast) or debootstrap (slow but more minimal).
# Set USE_DEBOOTSTRAP=1 to force debootstrap.
if [ "${USE_DEBOOTSTRAP:-}" = "1" ] && command -v debootstrap >/dev/null 2>&1; then
	echo "Building Ubuntu Noble rootfs via debootstrap..."
	debootstrap --variant=minbase --arch=amd64 noble "${TMP}/root" http://archive.ubuntu.com/ubuntu
else
	echo "Building Ubuntu Noble rootfs via cloud image download..."
	URL="https://cloud-images.ubuntu.com/minimal/releases/noble/release/ubuntu-24.04-minimal-cloudimg-amd64-root.tar.xz"
	curl -fSLo "${TMP}/ubuntu.tar.xz" "${URL}"
	tar xf "${TMP}/ubuntu.tar.xz" -C "${TMP}/root"
fi

# Set up bind mounts for chroot
mount --bind /dev "${TMP}/root/dev"
mount --bind /dev/pts "${TMP}/root/dev/pts"
mount -t proc proc "${TMP}/root/proc"
mount -t sysfs sysfs "${TMP}/root/sys"

# Ensure /etc/resolv.conf is a real file (cloud image may have a symlink)
rm -f "${TMP}/root/etc/resolv.conf" 2>/dev/null || true
cp "/etc/resolv.conf" "${TMP}/root/etc/resolv.conf"

chroot "${TMP}/root" bash -e <"builder/ubuntu-setup.sh"

# Unmount before creating squashfs
umount "${TMP}/root/sys" 2>/dev/null || true
umount "${TMP}/root/proc" 2>/dev/null || true
umount "${TMP}/root/dev/pts" 2>/dev/null || true
umount "${TMP}/root/dev" 2>/dev/null || true

mksquashfs "${TMP}/root" "/mnt/out/layer.squashfs" -noappend
