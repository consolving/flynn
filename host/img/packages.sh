#!/bin/bash

# install packages for starting flynn-host within an existing Flynn cluster
# either in a container or in a VM
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install --yes systemd udev zfsutils-linux iptables net-tools iproute2
apt-get clean

# install jq for reading container config files
JQ_URL="https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-amd64"
JQ_SHA="5942c9b0934e510ee61eb3e30273f1b3fe2590df93933a93d7c58b81d19c8ff5"
curl -fsSLo /tmp/jq "${JQ_URL}"
echo "${JQ_SHA}  /tmp/jq" | sha256sum -c -
mv /tmp/jq /usr/local/bin/jq
chmod +x /usr/local/bin/jq

# add a systemd service to start flynn-host in a VM
cat > /etc/systemd/system/flynn-host.service <<EOF
[Unit]
Description=Flynn host daemon
Documentation=https://flynn.io/docs
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/start-flynn-host.sh --systemd
Restart=on-failure

# set delegate yes so that systemd does not reset the cgroups of containers
Delegate=yes

# kill only the flynn-host process, not all processes in the cgroup
KillMode=process

# every container uses several fds, so make sure there are enough
LimitNOFILE=10000

[Install]
WantedBy=multi-user.target
EOF
systemctl enable flynn-host.service

# configure VM networking
cat > /etc/systemd/network/10-flynn.network <<EOF
[Match]
Name=en*

[Network]
DHCP=ipv4
EOF
systemctl enable systemd-networkd.service
