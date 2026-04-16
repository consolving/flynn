#!/bin/bash

export DEBIAN_FRONTEND=noninteractive

apt-get update
apt-get dist-upgrade -y
apt-get install -y redis-server
apt-get clean
apt-get autoremove -y
mkdir -p /data
