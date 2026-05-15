#!/bin/bash

export DEBIAN_FRONTEND=noninteractive

# Install prerequisites
apt-get update
apt-get install -y curl ca-certificates gnupg lsb-release

# Add PostgreSQL APT repository (PGDG) for Noble
install -d /usr/share/postgresql-common/pgdg
curl -fsSL https://www.postgresql.org/media/keys/ACCC4CF8.asc \
  -o /usr/share/postgresql-common/pgdg/apt.postgresql.org.asc
echo "deb [signed-by=/usr/share/postgresql-common/pgdg/apt.postgresql.org.asc] http://apt.postgresql.org/pub/repos/apt/ noble-pgdg main" \
  > /etc/apt/sources.list.d/pgdg.list

# Add TimescaleDB APT repository for Noble
curl -fsSL https://packagecloud.io/timescale/timescaledb/gpgkey \
  | gpg --dearmor -o /usr/share/keyrings/timescaledb-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/timescaledb-archive-keyring.gpg] https://packagecloud.io/timescale/timescaledb/ubuntu/ noble main" \
  > /etc/apt/sources.list.d/timescaledb.list

apt-get update
apt-get dist-upgrade -y
apt-get install -y -q \
  language-pack-en \
  less \
  sudo \
  postgresql-16 \
  postgresql-contrib \
  postgresql-16-postgis-3 \
  postgresql-16-pgrouting \
  postgresql-16-pgextwlist \
  timescaledb-2-postgresql-16
apt-get clean
apt-get autoremove -y

update-locale LANG=en_US.UTF-8 LANGUAGE=en_US.UTF-8 LC_ALL=en_US.UTF-8
dpkg-reconfigure locales

echo "\set HISTFILE /dev/null" > /root/.psqlrc
