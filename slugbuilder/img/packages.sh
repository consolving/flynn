#!/bin/bash
# Package install script for the slugbuilder image (run in chroot on the base
# layer by script/export-tuf). Installs the build-time tools and the pinned
# Heroku buildpacks, which are staged at /tmp/buildpacks.txt and
# /tmp/install-buildpack.
set -euo pipefail

export DEBIAN_FRONTEND=noninteractive

# git (install-buildpack), daemontools (setuidgid used by build.sh), pigz, jq,
# curl and ca-certificates (HTTPS buildpack clones and build cache).
apt-get update
apt-get install -y --no-install-recommends \
  git daemontools pigz jq curl ca-certificates
apt-get clean

# Install the pinned buildpacks into /builder/buildpacks in buildpacks.txt order.
mkdir -p /builder/buildpacks
nl -nrz /tmp/buildpacks.txt | awk '{print $2 "\t" $1}' | while read -r url order; do
  /tmp/install-buildpack /builder/buildpacks "$url" "$order" /tmp
done

# Allow the unprivileged flynn user to install custom buildpacks at build time.
chmod ugo+w /builder/buildpacks
