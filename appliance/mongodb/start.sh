#!/bin/bash

case $1 in
  mongodb)
    # Ensure mongodb user/group exist (base image may lack them)
    if ! id -u mongodb &>/dev/null; then
      groupadd -r mongodb 2>/dev/null || true
      useradd -r -g mongodb -d /data -s /bin/false mongodb 2>/dev/null || true
    fi
    chown -R mongodb:mongodb /data
    chmod 0700 /data
    shift
    exec sudo \
      -u mongodb \
      -E -H \
      /bin/flynn-mongodb $*
    ;;
  api)
    shift
    exec /bin/flynn-mongodb-api $*
    ;;
  *)
    echo "Usage: $0 {mongodb|api}"
    exit 2
    ;;
esac
