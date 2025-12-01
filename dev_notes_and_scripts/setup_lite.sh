#!/bin/bash

# We can't set -e because the post-installation of isc-dhcp-client pukes
# due to no systemd running. The install itself works though.
# 🤪

#set -e

echo "tzdata tzdata/Areas select Etc" | debconf-set-selections
echo "tzdata tzdata/Zones/Etc select UTC" | debconf-set-selections
apt update
DEBIAN_FRONTEND=noninteractive apt install -y systemd systemd-sysv dbus udev

apt install -y iproute2 iputils-ping telnet netcat-openbsd vim
apt install -y cloud-init cloud-initramfs-growroot

echo "isc-dhcp-client isc-dhcp-client/run-dhclient boolean false" | debconf-set-selections
#ln -sf /bin/true /usr/sbin/invoke-rc.d
DEBIAN_FRONTEND=noninteractive apt install -y --no-install-recommends isc-dhcp-client
rm -fr /var/lib/apt/lists/*

echo "root:root" | chpasswd

#mv /tmp/fcnet.service /etc/systemd/system
#mv /tmp/fcnet-setup.sh /usr/local/bin

#chown root:root /etc/systemd/system/fcnet.service
#chown root:root /usr/local/bin/fcnet-setup.sh
#chmod +x /usr/local/bin/fcnet-setup.sh
#systemctl disable systemd-resolved

#systemctl enable fcnet.service ### MAY NOT WORK due to error with sshd.service
#ln -s /etc/systemd/system/fcnet.service /etc/systemd/system/multi-user.target.wants/fcnet.service

#echo "nameserver 8.8.8.8" > /etc/resolv.conf
#echo "127.0.0.1 localhost" > /etc/hosts


SCRIPT_PATH="/usr/local/bin/setupnetwork.sh"
SERVICE_NAME="setupnetwork.service"

# --- 1. Create the startup script ---
cat << 'EOF' | tee "$SCRIPT_PATH" > /dev/null
#!/bin/sh
echo "Running startup script..."
ip link set dev eth0 up
dhclient eth0
EOF

chmod +x "$SCRIPT_PATH"

# --- 2. Create the systemd service ---
cat << EOF | tee "/etc/systemd/system/$SERVICE_NAME" > /dev/null
[Unit]
Description=Set up the network
After=network.target

[Service]
Type=oneshot
ExecStart=$SCRIPT_PATH

[Install]
WantedBy=multi-user.target
EOF

# --- 3. Enable the service ---
systemctl daemon-reload
systemctl enable "$SERVICE_NAME"

echo "Service $SERVICE_NAME created and enabled."
echo "Startup script located at $SCRIPT_PATH"

