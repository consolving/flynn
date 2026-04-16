#!/bin/bash

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y curl ca-certificates gnupg software-properties-common apt-transport-https

# Add MariaDB 10.11 LTS repository for Noble
curl -fsSL https://mariadb.org/mariadb_release_signing_key.pgp \
  -o /etc/apt/keyrings/mariadb-keyring.pgp
echo "deb [signed-by=/etc/apt/keyrings/mariadb-keyring.pgp] https://dlm.mariadb.com/repo/mariadb-server/10.11/repo/ubuntu noble main" \
  > /etc/apt/sources.list.d/mariadb.list

apt-get update
apt-get install -y sudo mariadb-server mariadb-backup
apt-get clean
apt-get autoremove -y
