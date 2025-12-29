#!/bin/bash

# We can't set -e because the post-installation of isc-dhcp-client pukes
# due to no systemd running. The install itself works though.
# 🤪
# Aaaand, we're not using that package after all.

set -e

echo "tzdata tzdata/Areas select Etc" | debconf-set-selections
echo "tzdata tzdata/Zones/Etc select UTC" | debconf-set-selections
apt update
DEBIAN_FRONTEND=noninteractive apt install -y systemd systemd-sysv dbus udev

apt install -y iproute2 iputils-ping telnet netcat-openbsd vim
apt install -y cloud-init cloud-initramfs-growroot


# This doesn't survive the reboot but we want it in place for the
# ssh install. The in-guest setup script will re-set it later.
echo "127.0.0.1 localhost" > /etc/hosts
apt install -y apt-utils
apt install -y openssh-server

# Don't use dhcp in the guest.
#echo "isc-dhcp-client isc-dhcp-client/run-dhclient boolean false" | debconf-set-selections
#DEBIAN_FRONTEND=noninteractive apt install -y --no-install-recommends isc-dhcp-client

rm -fr /var/lib/apt/lists/*

### BOTH OF THESE SHOULD BE DELETED!!!
#echo "root:root" | chpasswd
#echo "ubuntu:ubuntu" | chpasswd

#mv /tmp/fcnet.service /etc/systemd/system
#mv /tmp/fcnet-setup.sh /usr/local/bin

#chown root:root /etc/systemd/system/fcnet.service
#chown root:root /usr/local/bin/fcnet-setup.sh
#chmod +x /usr/local/bin/fcnet-setup.sh
#systemctl disable systemd-resolved

#systemctl enable fcnet.service ### MAY NOT WORK due to error with sshd.service
#ln -s /etc/systemd/system/fcnet.service /etc/systemd/system/multi-user.target.wants/fcnet.service

#echo "nameserver 8.8.8.8" > /etc/resolv.conf


SCRIPT_DIR="/usr/local/bin"
SCRIPT_PATH="$SCRIPT_DIR/setupnetwork.sh"
SERVICE_NAME="setupnetwork.service"

# shell MUST be bash because of the substitutions used
cat << 'EOF' | tee "$SCRIPT_PATH" > /dev/null
#!/bin/bash
INTERFACE="eth0"
MAC=$(ip link show "$INTERFACE" | awk '/ether/ {print $2}' | tr -d ':')
if [ -z "$MAC" ] || [ ${#MAC} -ne 12 ]; then
    echo "Error: Could not read a valid MAC address from $INTERFACE"
    exit 1
fi
LAST4="${MAC: -8}"
A=$((16#${LAST4:0:2}))
B=$((16#${LAST4:2:2}))
C=$((16#${LAST4:4:2}))
D=$((16#${LAST4:6:2}))
IP="$A.$B.$C.$D"
echo "MAC $MAC  →  last 4 octets $LAST4  →  IP $IP"
ip addr flush dev "$INTERFACE" 2>/dev/null
ip addr add "$IP/16" dev "$INTERFACE"
ip link set "$INTERFACE" up
ip ro add default via 10.0.0.1
echo "127.0.0.1 localhost" > /etc/hosts
EOF

chmod +x "$SCRIPT_PATH"
chmod 0700 "$SCRIPT_DIR"
chmod 0700 "$SCRIPT_PATH"

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
# systemctl daemon-reload (fails in a docker container)
systemctl enable "$SERVICE_NAME"

echo "Service $SERVICE_NAME created and enabled."
echo "Startup script located at $SCRIPT_PATH"








# setup the guest daemon. OBSOLETE.

#GUEST_DAEMON_SERVICE_NAME="guestdaemon.service"
#cat << EOF | tee "/etc/systemd/system/$GUEST_DAEMON_SERVICE_NAME" > /dev/null
#[Unit]
#Description=Guest Daemon Service
#After=setupnetwork.service
#Requires=setupnetwork.service
#
#[Service]
#ExecStart=/usr/local/bin/guest_daemon
#Restart=always
#RestartSec=1
#StopSignal=SIGTERM
#
#[Install]
#WantedBy=multi-user.target
#EOF

#systemctl enable "$GUEST_DAEMON_SERVICE_NAME"



# setup the guest-sentences daemon

GUEST_SENTENCES_SERVICE_NAME="guestsentences.service"
cat << EOF | tee "/etc/systemd/system/$GUEST_SENTENCES_SERVICE_NAME" > /dev/null
[Unit]
Description=Guest Sentences Service
After=setupnetwork.service
Requires=setupnetwork.service

[Service]
ExecStart=/usr/local/bin/guest_sentences
Restart=always
RestartSec=1
StopSignal=SIGTERM

[Install]
WantedBy=multi-user.target
EOF

systemctl enable "$GUEST_SENTENCES_SERVICE_NAME"





# setup the identity file (needs to be edited for each instance)
# Leave this in for now although it's not currently used.
# Personalizing it per-agent is a pain because of the need to mount
# it loopback.
# The agent-id value and slot number are passed in through firecracker
# as Linux boot parameter, and the other parms aren't as critical

mkdir -p /.ngen
cat << EOF | tee "/.ngen/.id" > /dev/null
{
 "host":"1000",
 "agent":"1000",
 "slot":1000,
 "nats-server":"nats://192.168.0.225:4222"
}
EOF


# Now set up a systemd service for each of the apps.


APP_DIR=/apps

if [[ -z "$APP_DIR" ]]; then
  echo "Usage: $0 <directory>"
  exit 1
fi

if [[ ! -d "$APP_DIR" ]]; then
  echo "Directory does not exist: $APP_DIR"
  exit 1
fi

SYSTEMD_DIR="/etc/systemd/system"

echo "Scanning executables in $APP_DIR..."

for file in "$APP_DIR"/*; do
  if [[ -f "$file" && -x "$file" ]]; then
    name=$(basename "$file")
    service_name="${name}.service"
    service_path="$SYSTEMD_DIR/$service_name"

    echo "Creating service: $service_name"

    cat > "$service_path" <<EOF
[Unit]
Description=Service for $name
After=network.target

[Service]
Type=simple
User=ubuntu
WorkingDirectory=$APP_DIR
ExecStart=$file
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

    systemctl enable "$service_name"
    # systemctl start "$service_name"
  fi
done

echo "Reloading systemd..."
#systemctl daemon-reexec
#systemctl daemon-reload
echo "All services created and started."


# Next, set up a place for selkirk sentences, with wide permissions
mkdir -p /opt/agentsentences
chmod 0777 /opt/agentsentences




