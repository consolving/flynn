#!/bin/bash

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get install -y curl ca-certificates gnupg

# Add MongoDB 7.0 repository for Noble
curl -fsSL https://www.mongodb.org/static/pgp/server-7.0.asc \
  -o /etc/apt/keyrings/mongodb-server-7.0.gpg
echo "deb [signed-by=/etc/apt/keyrings/mongodb-server-7.0.gpg] https://repo.mongodb.org/apt/ubuntu noble/mongodb-org/7.0 multiverse" \
  > /etc/apt/sources.list.d/mongodb-org-7.0.list

apt-get update
apt-get install -y sudo mongodb-org
apt-get clean
apt-get autoremove -y
