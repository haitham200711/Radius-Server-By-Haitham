#!/bin/bash
set -e
echo "=== Starting Accel-PPP SSTP Build ==="
rm -rf /opt/accel-ppp-code
git clone --depth 1 https://github.com/accel-ppp/accel-ppp.git /opt/accel-ppp-code
sed -i 's/if (conn->nonce && memcmp(msg->attr.nonce, conn->nonce, SSTP_NONCE_SIZE) != 0) {/if (0) {/' /opt/accel-ppp-code/accel-pppd/ctrl/sstp/sstp.c
mkdir -p /opt/accel-ppp-code/build
cd /opt/accel-ppp-code/build
cmake -DBUILD_IPOE_DRIVER=FALSE -DBUILD_VLAN_MON_DRIVER=FALSE -DCMAKE_INSTALL_PREFIX=/usr/local ..
make -j$(nproc)
make install
echo "net.ipv4.ip_forward = 1" >> /etc/sysctl.conf
sysctl -p

cat << 'SEOF' > /etc/systemd/system/accel-ppp.service
[Unit]
Description=Accel-PPP SSTP VPN Server
After=network.target

[Service]
Type=forking
ExecStart=/usr/local/sbin/accel-pppd -d -c /etc/accel-ppp.conf
ExecStop=/usr/bin/pkill accel-pppd
Restart=always

[Install]
WantedBy=multi-user.target
SEOF

systemctl daemon-reload
systemctl enable accel-ppp
echo "=== Accel-PPP SSTP Build Complete ==="
