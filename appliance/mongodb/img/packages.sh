#!/bin/bash
set -eo pipefail

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y curl ca-certificates gnupg

# Add MongoDB 8.0 repository for Noble.
# NOTE: MongoDB 7.0 does NOT publish packages for Ubuntu 24.04 (noble) —
# https://repo.mongodb.org/apt/ubuntu/dists/noble/mongodb-org/7.0/ is empty.
# 8.0 is the first release line with noble support. Using 7.0 here silently
# produced an image with no mongod binary at all (apt-get install failed,
# but without `set -e` the script kept going and still exited 0).
curl -fsSL https://www.mongodb.org/static/pgp/server-8.0.asc \
  | gpg --dearmor -o /etc/apt/keyrings/mongodb-server-8.0.gpg
echo "deb [signed-by=/etc/apt/keyrings/mongodb-server-8.0.gpg] https://repo.mongodb.org/apt/ubuntu noble/mongodb-org/8.0 multiverse" \
  > /etc/apt/sources.list.d/mongodb-org-8.0.list

apt-get update
apt-get install -y sudo mongodb-org
apt-get clean
apt-get autoremove -y

# Fail loudly if the actual server binary didn't get installed, instead of
# silently producing a broken image.
test -x /usr/bin/mongod || { echo "ERROR: /usr/bin/mongod missing after install" >&2; exit 1; }
