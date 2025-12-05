#!/bin/bash
# /usr/local/bin/create-firecracker-taps.sh
for try in {1..30}; do
    [[ -d /sys/class/net/fire0 ]] && break
    echo "Waiting for fire0 to appear ($try/30)..."
    sleep 1
done

[[ ! -d /sys/class/net/fire0 ]] && { echo "fire0 never appeared"; exit 1; }

for i in {0..100}; do
    ip tuntap add dev tap$i mode tap user francis 2>/dev/null || break
    ip link set tap$i master fire0 up 2>/dev/null || sleep 0.1
done

sleep 1
#systemctl start nftables.service

modprobe br_netfilter
sysctl -w net.bridge.bridge-nf-call-iptables=1
sysctl -w net.bridge.bridge-nf-call-ip6tables=1
sysctl -w net.bridge.bridge-nf-call-arpables=1
iptables -t nat -A POSTROUTING -o eno1 -s 10.0.0.0/16 -j MASQUERADE
iptables -A FORWARD -i fire0 -o eno1 -j ACCEPT
iptables -A FORWARD -i eno1 -o fire0 -m state --state RELATED,ESTABLISHED -j ACCEPT
