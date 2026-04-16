#!/bin/bash

set -eo pipefail

go_version="1.22.12"
go_shasum="4fa4f869b0f7fc6bb1eb2660e74657fbf04cdd290b5aef905585c86051b34d43"
gobin_commit="ef6664e41f0bfe3007869844d318bb2bfa2627f9"
dir="/usr/local"

apt-get update
apt-get install --yes git build-essential
apt-get clean

curl -fsSLo /tmp/go.tar.gz "https://go.dev/dl/go${go_version}.linux-amd64.tar.gz"
echo "${go_shasum}  /tmp/go.tar.gz" | sha256sum -c -
rm -rf "${dir}/go"
tar xzf /tmp/go.tar.gz -C "${dir}"
rm /tmp/go.tar.gz

export GOROOT="/usr/local/go"
export GOPATH="/go"
export PATH="${GOROOT}/bin:${PATH}"

cp "builder/go-wrapper.sh" "/usr/local/bin/go"
cp "builder/go-wrapper.sh" "/usr/local/bin/cgo"
cp "builder/go-wrapper.sh" "/usr/local/bin/gobin"

# install gobin
git clone https://github.com/flynn/gobin "/tmp/gobin"
trap "rm -rf /tmp/gobin" EXIT
cd "/tmp/gobin"
git reset --hard ${gobin_commit}
/usr/local/bin/go build -o /usr/local/bin/gobin-noenv
