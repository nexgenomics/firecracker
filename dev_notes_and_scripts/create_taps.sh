#!/bin/bash
# /usr/local/bin/create-firecracker-taps.sh
# This script runs under systemd and creates tap interfaces

for try in {1..30}; do
    [[ -d /sys/class/net/fire0 ]] && break
    echo "Waiting for fire0 to appear ($try/30)..."
    sleep 1
done

[[ ! -d /sys/class/net/fire0 ]] && { echo "fire0 never appeared"; exit 1; }

for i in {0..64}; do
    ip tuntap add dev tap$i mode tap user francis 2>/dev/null || break
    ip link set tap$i master fire0 up 2>/dev/null || sleep 0.1
done

#sleep 1
#systemctl start nftables.service
