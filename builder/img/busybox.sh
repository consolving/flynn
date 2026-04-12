#!/bin/bash
#
# Build a minimal busybox rootfs as a squashfs layer.
#
# Requires busybox-static to be installed on the host system:
#   apt-get install -y busybox-static
#

set -e

TMP="$(mktemp --directory)"
trap "rm -rf '${TMP}'" EXIT

# Use the system-installed busybox-static binary
BUSYBOX="$(which busybox)"
if [[ ! -x "${BUSYBOX}" ]]; then
	echo "ERROR: busybox-static not found. Install with: apt-get install -y busybox-static" >&2
	exit 1
fi

mkdir "${TMP}/root"
cd "${TMP}/root"
mkdir bin etc dev dev/pts lib proc sys tmp
touch etc/resolv.conf
cp /etc/nsswitch.conf etc/nsswitch.conf
echo root:x:0:0:root:/:/bin/sh >etc/passwd
echo root:x:0: >etc/group
ln -s lib lib64
ln -s bin sbin
cp "${BUSYBOX}" bin/busybox
for name in $(busybox --list); do
	[[ "${name}" = "busybox" ]] && continue
	ln -s busybox "bin/${name}"
done
cp /lib/x86_64-linux-gnu/lib{c,dl,nsl,nss_*,pthread,resolv}.so.* lib 2>/dev/null || true
cp /lib/x86_64-linux-gnu/ld-linux-x86-64.so.2 lib 2>/dev/null || true

mksquashfs "${TMP}/root" "/mnt/out/layer.squashfs" -noappend
