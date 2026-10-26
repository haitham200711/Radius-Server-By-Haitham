#!/usr/bin/env bash
# ==============================================================================
#   HAITHAM RADIUS ENTERPRISE - ONE-CLICK COMPLETE INSTALLER
#   Author: Haitham Abd-Elkader
#   GitHub: https://github.com/haitham200711/Radius-Server-By-Haitham
#   Supported OS: Ubuntu 20.04 / 22.04 / 24.04 & Debian 11 / 12 (x86_64)
# ==============================================================================

set -eo pipefail

# Colors for terminal styling
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

[ -t 1 ] && clear 2>/dev/null || true

echo -e "${CYAN}${BOLD}"
cat << "EOF"
██╗  ██╗ █████╗ ██╗████████╗██╗  ██╗ █████╗ ███╗   ███╗
██║  ██║██╔══██╗██║╚══██╔══╝██║  ██║██╔══██╗████╗ ████║
███████║███████║██║   ██║   ███████║███████║██╔████╔██║
██╔══██║██╔══██║██║   ██║   ██╔══██║██╔══██║██║╚██╔╝██║
██║  ██║██║  ██║██║   ██║   ██║  ██║██║  ██║██║ ╚═╝ ██║
╚═╝  ╚═╝╚═╝  ╚═╝╚═╝   ╚═╝   ╚═╝  ╚═╝╚═╝  ╚═╝╚═╝     ╚═╝
EOF
echo -e "${GREEN}   HAITHAM RADIUS ENTERPRISE - ONE-CLICK INSTALLER${NC}"
echo -e "${YELLOW}   FreeRADIUS v3 + MariaDB + Accel-PPP SSTP + Go Platform${NC}"
echo -e "${BLUE}===================================================================${NC}"
echo ""

# 1. Root & OS Verification
if [[ $EUID -ne 0 ]]; then
   echo -e "${RED}[ERROR] This installation script must be run as root or with sudo.${NC}"
   echo -e "${YELLOW}Usage: sudo bash install.sh${NC}"
   exit 1
fi

export DEBIAN_FRONTEND=noninteractive
export NEEDRESTART_MODE=a
export NEEDRESTART_SUSPEND=1
if [[ -f /etc/needrestart/needrestart.conf ]]; then
    sed -i "s/#\$nrconf{restart} = 'i';/\$nrconf{restart} = 'a';/g" /etc/needrestart/needrestart.conf 2>/dev/null || true
    sed -i "s/\$nrconf{restart} = 'i';/\$nrconf{restart} = 'a';/g" /etc/needrestart/needrestart.conf 2>/dev/null || true
fi

echo -e "${CYAN}[INFO] Checking system architecture and OS...${NC}"
ARCH=$(uname -m)
if [[ "$ARCH" != "x86_64" ]]; then
    echo -e "${RED}[ERROR] Unsupported architecture: $ARCH. Haitham RADIUS requires x86_64 (amd64).${NC}"
    exit 1
fi

INSTALL_DIR="/opt/radius-server"
REPO_URL="https://github.com/haitham200711/Radius-Server-By-Haitham.git"
DB_NAME="radiusdb"
DB_USER="radius"

# Detect Go version from go.mod if present, fallback to stable 1.22.8
GO_VERSION="1.22.8"
if [[ -f "go.mod" ]]; then
    DETECTED_GO=$(grep -E '^go [0-9]+\.[0-9]+' go.mod | awk '{print $2}' | tr -d '\r\n')
    if [[ -n "$DETECTED_GO" ]]; then
        GO_VERSION="${DETECTED_GO}.0"
    fi
fi

# Load or generate cryptographically secure database password
ENV_FILE="$INSTALL_DIR/.env"
if [[ -f "$ENV_FILE" ]] && grep -q "DB_DSN=" "$ENV_FILE" 2>/dev/null; then
    DB_PASS=$(grep "DB_DSN=" "$ENV_FILE" | sed -E 's/.*:([^@]+)@.*/\1/' | head -n1)
fi

if [[ -z "$DB_PASS" ]]; then
    DB_PASS=$(openssl rand -hex 16 2>/dev/null || tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 24)
fi

# Detect Public / Local IP
SERVER_IP=$(hostname -I | awk '{print $1}')
if [[ -z "$SERVER_IP" ]]; then
    SERVER_IP="127.0.0.1"
fi

echo -e "${GREEN}[OK] Detected Server IP: ${BOLD}${SERVER_IP}${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 1: System Packages & Build Tools
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 1/8] Updating System & Installing Core Dependencies...${NC}"
echo -e "${BLUE}===================================================================${NC}"

apt-get update -y
apt-get install -y --no-install-recommends \
    curl \
    wget \
    git \
    build-essential \
    cmake \
    gcc \
    make \
    pkg-config \
    libssl-dev \
    libpcre3-dev \
    libnl-3-dev \
    libnl-genl-3-dev \
    ssl-cert \
    net-tools \
    iproute2 \
    iptables \
    tar \
    ca-certificates \
    jq

echo -e "${GREEN}[OK] Core dependencies installed successfully.${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 2: MariaDB Database Setup
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 2/8] Installing & Configuring MariaDB Database...${NC}"
echo -e "${BLUE}===================================================================${NC}"

apt-get install -y mariadb-server mariadb-client
systemctl enable mariadb
systemctl start mariadb

echo -e "${CYAN}[INFO] Creating database and radius user...${NC}"
MYSQL_CMD="mariadb"
if ! command -v mariadb &>/dev/null; then
    MYSQL_CMD="mysql"
fi

$MYSQL_CMD -e "CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"
$MYSQL_CMD -e "CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASS}';"
$MYSQL_CMD -e "GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'localhost';"
$MYSQL_CMD -e "CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASS}';"
$MYSQL_CMD -e "GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';"
$MYSQL_CMD -e "FLUSH PRIVILEGES;"

echo -e "${GREEN}[OK] MariaDB database '${DB_NAME}' initialized.${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 3: FreeRADIUS v3 Engine Setup
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 3/8] Installing & Configuring FreeRADIUS v3 Engine...${NC}"
echo -e "${BLUE}===================================================================${NC}"

apt-get install -y freeradius freeradius-mysql freeradius-utils

# Check if tables exist; if not, import FreeRADIUS standard schema
TABLE_COUNT=$($MYSQL_CMD -u ${DB_USER} -p${DB_PASS} -D ${DB_NAME} -sN -e "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = '${DB_NAME}' AND table_name = 'radcheck';" 2>/dev/null || echo "0")
if [[ "$TABLE_COUNT" -eq "0" ]]; then
    echo -e "${CYAN}[INFO] Importing standard FreeRADIUS MySQL schema...${NC}"
    SCHEMA_PATH=""
    if [[ -f "/etc/freeradius/3.0/mods-config/sql/main/mysql/schema.sql" ]]; then
        SCHEMA_PATH="/etc/freeradius/3.0/mods-config/sql/main/mysql/schema.sql"
    elif [[ -f "/etc/freeradius/mods-config/sql/main/mysql/schema.sql" ]]; then
        SCHEMA_PATH="/etc/freeradius/mods-config/sql/main/mysql/schema.sql"
    else
        SCHEMA_PATH=$(find /etc/freeradius* /usr/share/freeradius* -name "schema.sql" 2>/dev/null | grep -E "main/mysql|mysql/schema" | head -n1 || true)
    fi
    if [[ -n "$SCHEMA_PATH" && -f "$SCHEMA_PATH" ]]; then
        echo -e "${CYAN}[INFO] Found FreeRADIUS schema at: ${SCHEMA_PATH}${NC}"
        $MYSQL_CMD -u ${DB_USER} -p${DB_PASS} -D ${DB_NAME} < "$SCHEMA_PATH"
    fi
fi

# Configure FreeRADIUS SQL module
SQL_CONF="/etc/freeradius/3.0/mods-available/sql"
if [[ -f "$SQL_CONF" ]]; then
    echo -e "${CYAN}[INFO] Configuring ${SQL_CONF}...${NC}"
    sed -i 's/^[[:space:]]*dialect = .*/\tdialect = "mysql"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*driver = .*/\tdriver = "rlm_sql_mysql"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*server = .*/\tserver = "127.0.0.1"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*port = .*/\tport = 3306/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*login = .*/\tlogin = "'"${DB_USER}"'"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*password = .*/\tpassword = "'"${DB_PASS}"'"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*radius_db = .*/\tradius_db = "'"${DB_NAME}"'"/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*read_clients = .*/\tread_clients = yes/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*client_table = .*/\tclient_table = "nas"/' "$SQL_CONF"

    # Disable TLS config in sql file to avoid connection errors on localhost
    sed -i 's/^[[:space:]]*ca_file =/#\tca_file =/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*ca_path =/#\tca_path =/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*certificate_file =/#\tcertificate_file =/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*private_key_file =/#\tprivate_key_file =/' "$SQL_CONF"
    sed -i 's/^[[:space:]]*tls_required =/#\ttls_required =/' "$SQL_CONF"

    # Enable sql module symlink
    ln -sf /etc/freeradius/3.0/mods-available/sql /etc/freeradius/3.0/mods-enabled/sql
fi

# Enable SQL in default site and inner-tunnel site
DEFAULT_SITE="/etc/freeradius/3.0/sites-available/default"
if [[ -f "$DEFAULT_SITE" ]]; then
    echo -e "${CYAN}[INFO] Enabling SQL handlers in sites-available/default...${NC}"
    sed -i '/authorize {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
    sed -i '/accounting {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
    sed -i '/session {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
    sed -i '/post-auth {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
fi

INNER_TUNNEL="/etc/freeradius/3.0/sites-available/inner-tunnel"
if [[ -f "$INNER_TUNNEL" ]]; then
    echo -e "${CYAN}[INFO] Enabling SQL handlers in sites-available/inner-tunnel...${NC}"
    sed -i '/authorize {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$INNER_TUNNEL"
    sed -i '/session {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$INNER_TUNNEL"
    sed -i '/post-auth {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$INNER_TUNNEL"
fi

# Set proper ownership and restart
chown -R freerad:freerad /etc/freeradius/3.0/ 2>/dev/null || true
systemctl restart freeradius
systemctl enable freeradius

echo -e "${GREEN}[OK] FreeRADIUS v3 is active and connected to MariaDB.${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 4: Accel-PPP SSTP VPN Server
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 4/8] Building & Configuring Accel-PPP SSTP Server...${NC}"
echo -e "${BLUE}===================================================================${NC}"

if ! command -v accel-pppd &>/dev/null; then
    echo -e "${CYAN}[INFO] Cloning and building Accel-PPP with MikroTik SSTP compatibility patch...${NC}"
    rm -rf /tmp/accel-ppp-code
    git clone --depth 1 https://github.com/accel-ppp/accel-ppp.git /tmp/accel-ppp-code
    
    # Apply MikroTik SSTP Nonce compatibility patch
    sed -i 's/if (conn->nonce && memcmp(msg->attr.nonce, conn->nonce, SSTP_NONCE_SIZE) != 0) {/if (0) {/' /tmp/accel-ppp-code/accel-pppd/ctrl/sstp/sstp.c
    
    mkdir -p /tmp/accel-ppp-code/build
    cd /tmp/accel-ppp-code/build
    cmake -DBUILD_IPOE_DRIVER=FALSE -DBUILD_VLAN_MON_DRIVER=FALSE -DCMAKE_INSTALL_PREFIX=/usr/local ..
    make -j"$(nproc)"
    make install
    rm -rf /tmp/accel-ppp-code
    cd /
else
    echo -e "${GREEN}[OK] Accel-PPP is already installed at $(command -v accel-pppd).${NC}"
fi

# Ensure Snakeoil SSL certificates exist
if [[ ! -f "/etc/ssl/certs/ssl-cert-snakeoil.pem" || ! -f "/etc/ssl/private/ssl-cert-snakeoil.key" ]]; then
    echo -e "${CYAN}[INFO] Generating Snakeoil SSL certificates for SSTP...${NC}"
    make-ssl-cert generate-default-snakeoil --force-overwrite 2>/dev/null || {
        openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
            -keyout /etc/ssl/private/ssl-cert-snakeoil.key \
            -out /etc/ssl/certs/ssl-cert-snakeoil.pem \
            -subj "/C=EG/ST=Cairo/L=Cairo/O=HaithamRADIUS/CN=radius.local"
    }
fi

# Accel-PPP configuration
cat << 'EOF' > /etc/accel-ppp.conf
[modules]
sstp
auth_chap_md5
auth_pap
auth_mschap_v1
auth_mschap_v2
chap-secrets
ippool
log_file

[core]
thread-count=2

[cli]
verbose=1
tcp=127.0.0.1:2001

[sstp]
verbose=1
accept=ssl
bind=0.0.0.0
port=4444
ssl-pemfile=/etc/ssl/certs/ssl-cert-snakeoil.pem
ssl-keyfile=/etc/ssl/private/ssl-cert-snakeoil.key
mppe=require

[ppp]
ip-up=/etc/ppp/ip-up
verbose=1
min-mtu=1280
mtu=1400
mru=1400
ipv4=require
ipv6=deny
lcp-echo-interval=20
lcp-echo-failure=3

[client-ip-range]
0.0.0.0/0

[ip-pool]
gw-ip-address=10.171.171.1
10.171.171.2-255

[chap-secrets]
gw-ip-address=10.171.171.1
chap-secrets=/etc/ppp/chap-secrets

[log]
log-file=/var/log/accel-ppp/accel-ppp.log
log-emerg=/var/log/accel-ppp/emerg.log
log-fail-file=/var/log/accel-ppp/auth-fail.log
copy=1
level=3
EOF

mkdir -p /var/log/accel-ppp /etc/ppp
touch /etc/ppp/chap-secrets

# SSTP Dynamic Hook Script
cat << 'EOF' > /etc/ppp/ip-up
#!/bin/bash
curl -k -s "https://127.0.0.1/sstp/routes/hook-up" >/dev/null 2>&1 || curl -s "http://127.0.0.1/sstp/routes/hook-up" >/dev/null 2>&1 || true
EOF
chmod +x /etc/ppp/ip-up

# Enable IPv4 Forwarding
sysctl -w net.ipv4.ip_forward=1 >/dev/null
if ! grep -q "^net.ipv4.ip_forward = 1" /etc/sysctl.conf 2>/dev/null; then
    echo "net.ipv4.ip_forward = 1" >> /etc/sysctl.conf
fi

# Enable NAT Masquerade for SSTP VPN subnet if iptables is available
if command -v iptables &>/dev/null; then
    iptables -t nat -C POSTROUTING -s 10.171.171.0/24 ! -d 10.171.171.0/24 -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s 10.171.171.0/24 ! -d 10.171.171.0/24 -j MASQUERADE 2>/dev/null || true
fi

ACCEL_BIN=$(command -v accel-pppd || echo "/usr/local/sbin/accel-pppd")

# Accel-PPP Service
cat << EOF > /etc/systemd/system/accel-ppp.service
[Unit]
Description=Accel-PPP SSTP VPN Server
After=network.target

[Service]
Type=forking
ExecStart=${ACCEL_BIN} -d -c /etc/accel-ppp.conf
ExecStop=/usr/bin/pkill accel-pppd
Restart=always

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable accel-ppp
systemctl restart accel-ppp || true

echo -e "${GREEN}[OK] Accel-PPP SSTP server configured and started.${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 5: Go Compiler Environment
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 5/8] Setting Up Go Environment...${NC}"
echo -e "${BLUE}===================================================================${NC}"

NEED_GO_INSTALL=false
if ! command -v go &>/dev/null; then
    NEED_GO_INSTALL=true
else
    CURRENT_GO_VER=$(go version | awk '{print $3}' | sed 's/go//')
    # If version is older than 1.22, install official
    if [[ $(echo -e "1.22.0\n${CURRENT_GO_VER}" | sort -V | head -n1) != "1.22.0" ]]; then
        NEED_GO_INSTALL=true
    fi
fi

if [[ "$NEED_GO_INSTALL" = true ]]; then
    echo -e "${CYAN}[INFO] Downloading and installing official Go v${GO_VERSION}...${NC}"
    wget -q "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -O /tmp/go.tar.gz
    rm -rf /usr/local/go
    tar -C /usr/local -xzf /tmp/go.tar.gz
    rm -f /tmp/go.tar.gz
    ln -sf /usr/local/go/bin/go /usr/bin/go 2>/dev/null || true
    ln -sf /usr/local/go/bin/gofmt /usr/bin/gofmt 2>/dev/null || true
    export PATH=$PATH:/usr/local/go/bin
    if ! grep -q "/usr/local/go/bin" /etc/profile 2>/dev/null; then
        echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
    fi
fi

ln -sf /usr/local/go/bin/go /usr/bin/go 2>/dev/null || true
export PATH=$PATH:/usr/local/go/bin
echo -e "${GREEN}[OK] Go runtime: $(go version)${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 6: Deploying Haitham RADIUS Control-Plane
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 6/8] Compiling & Deploying Control-Plane Platform...${NC}"
echo -e "${BLUE}===================================================================${NC}"

# Stop running service if active to avoid "text file busy" when overwriting binary
systemctl stop radius-server 2>/dev/null || true

CURRENT_SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" >/dev/null 2>&1 && pwd )"

if [[ "$CURRENT_SCRIPT_DIR" = "$INSTALL_DIR" || -f "$CURRENT_SCRIPT_DIR/go.mod" ]]; then
    echo -e "${CYAN}[INFO] Installing from local source tree: ${CURRENT_SCRIPT_DIR}...${NC}"
    mkdir -p "$INSTALL_DIR"
    if [[ "$CURRENT_SCRIPT_DIR" != "$INSTALL_DIR" ]]; then
        cp -a "$CURRENT_SCRIPT_DIR"/. "$INSTALL_DIR"/
    fi
    cd "$INSTALL_DIR"
    echo -e "${CYAN}[INFO] Building Go binary: radius-platform...${NC}"
    go build -ldflags="-s -w" -o "$INSTALL_DIR/radius-platform" ./cmd/server
    chmod +x "$INSTALL_DIR/radius-platform"
elif [[ -d "$INSTALL_DIR/.git" ]]; then
    echo -e "${CYAN}[INFO] Updating existing git repository in ${INSTALL_DIR}...${NC}"
    cd "$INSTALL_DIR"
    git pull origin master 2>/dev/null || true
    if [[ -f "$INSTALL_DIR/go.mod" ]]; then
        go build -ldflags="-s -w" -o "$INSTALL_DIR/radius-platform" ./cmd/server
        chmod +x "$INSTALL_DIR/radius-platform"
    fi
else
    echo -e "${CYAN}[INFO] Downloading latest repository to ${INSTALL_DIR}...${NC}"
    TMP_CLONE="/tmp/radius-clone-temp"
    rm -rf "$TMP_CLONE"
    CLONE_SUCCESS=false
    
    # Check if GITHUB_TOKEN or GH_TOKEN is provided or clone anonymously
    CLONE_TARGET="$REPO_URL"
    TOKEN="${GITHUB_TOKEN:-$GH_TOKEN}"
    if [[ -n "$TOKEN" ]]; then
        CLONE_TARGET="https://${TOKEN}@github.com/haitham200711/Radius-Server-By-Haitham.git"
    fi

    if git clone --depth 1 "$CLONE_TARGET" "$TMP_CLONE" 2>/dev/null; then
        CLONE_SUCCESS=true
        mkdir -p "$INSTALL_DIR"
        cp -a "$TMP_CLONE"/. "$INSTALL_DIR"/
        rm -rf "$TMP_CLONE"
        cd "$INSTALL_DIR"
        echo -e "${CYAN}[INFO] Building Go binary: radius-platform...${NC}"
        go build -ldflags="-s -w" -o "$INSTALL_DIR/radius-platform" ./cmd/server
        chmod +x "$INSTALL_DIR/radius-platform"
    else
        echo -e "${YELLOW}[WARN] Could not clone repository directly (Repo may be Private on GitHub).${NC}"
        if [[ -f "$INSTALL_DIR/radius-platform" ]]; then
            echo -e "${GREEN}[OK] Found existing binary at ${INSTALL_DIR}/radius-platform. Proceeding with service setup...${NC}"
        else
            echo -e "${RED}[ERROR] Repository clone failed and no existing binary found.${NC}"
            echo -e "${YELLOW}If your repository is Private, you have 2 easy solutions:${NC}"
            echo -e "  1. Make the repo Public on GitHub (Settings -> Change repository visibility -> Make public)."
            echo -e "  2. Or run: export GITHUB_TOKEN=ghp_your_token ; curl -fsSL ... | sudo -E bash"
            exit 1
        fi
    fi
fi

mkdir -p "$INSTALL_DIR/uploads" "$INSTALL_DIR/static" "$INSTALL_DIR/templates" "$INSTALL_DIR/backups"
chmod -R 755 "$INSTALL_DIR"
chmod 750 "$INSTALL_DIR/uploads" 2>/dev/null || true
chmod 700 "$INSTALL_DIR/backups" 2>/dev/null || true

# Securely initialize .env if not already configured
ENV_FILE="$INSTALL_DIR/.env"
if [[ ! -f "$ENV_FILE" ]]; then
    echo -e "${CYAN}[INFO] Initializing secure credentials in $ENV_FILE (0600)...${NC}"
    RAND_SESSION_SECRET=$(openssl rand -hex 32 2>/dev/null || tr -dc 'a-f0-9' < /dev/urandom | head -c 64)
    if [[ -z "$INITIAL_ADMIN_PASSWORD" ]]; then
        INITIAL_ADMIN_PASSWORD=$(openssl rand -base64 12 2>/dev/null || tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 16)
    fi
    RAND_NAS_SECRET=$(openssl rand -hex 16 2>/dev/null || tr -dc 'a-zA-Z0-9' < /dev/urandom | head -c 24)
    cat << EOF > "$ENV_FILE"
SESSION_SECRET=${RAND_SESSION_SECRET}
INITIAL_ADMIN_PASSWORD=${INITIAL_ADMIN_PASSWORD}
DB_DRIVER=mysql
DB_DSN=${DB_USER}:${DB_PASS}@tcp(127.0.0.1:3306)/${DB_NAME}?parseTime=true&charset=utf8mb4
RADIUS_SECRET=${RAND_NAS_SECRET}
EOF
    chmod 600 "$ENV_FILE"
    echo -e "${GREEN}[OK] Secure .env file initialized with 0600 permissions.${NC}"
fi

echo -e "${GREEN}[OK] Binary compiled successfully: $INSTALL_DIR/radius-platform${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 7: Systemd Service & Firewall
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 7/8] Configuring Systemd Service & Firewall...${NC}"
echo -e "${BLUE}===================================================================${NC}"

cat << 'EOF' > /etc/systemd/system/radius-server.service
[Unit]
Description=Haitham RADIUS Enterprise Platform & Dashboard
After=network.target mariadb.service freeradius.service

[Service]
Type=simple
User=root
WorkingDirectory=/opt/radius-server
EnvironmentFile=/opt/radius-server/.env
Environment=WEB_ADDR=:443
Environment=HTTP_REDIRECT_ADDR=:80
Environment=ENABLE_HTTPS=true
ExecStart=/opt/radius-server/radius-platform
Restart=always
RestartSec=3
LimitNOFILE=65535
ProtectSystem=full
ProtectHome=true
NoNewPrivileges=true
PrivateTmp=true
RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable radius-server
systemctl restart radius-server

# Open Firewall Ports if UFW is active
if command -v ufw &>/dev/null && ufw status | grep -q "Status: active"; then
    echo -e "${CYAN}[INFO] Configuring UFW firewall rules...${NC}"
    ufw allow 443/tcp comment 'RADIUS Web Control-Plane HTTPS'
    ufw allow 80/tcp comment 'RADIUS Web HTTP Redirect'
    ufw allow 5000/tcp comment 'RADIUS Web Legacy Port'
    ufw allow 1812/udp comment 'FreeRADIUS Authentication'
    ufw allow 1813/udp comment 'FreeRADIUS Accounting'
    ufw allow 3799/udp comment 'FreeRADIUS CoA / Disconnect'
    ufw allow 4444/tcp comment 'Accel-PPP SSTP VPN'
fi

echo -e "${GREEN}[OK] Service installed and started.${NC}"
echo ""

# ------------------------------------------------------------------------------
# STEP 8: Final Health Check & Verification
# ------------------------------------------------------------------------------
echo -e "${BLUE}===================================================================${NC}"
echo -e "${CYAN}${BOLD}[STEP 8/8] Verifying Services Health...${NC}"
echo -e "${BLUE}===================================================================${NC}"

sleep 3

check_service() {
    local srv="$1"
    if systemctl is-active --quiet "$srv"; then
        echo -e "  [✔] Service ${BOLD}${srv}${NC}: ${GREEN}ACTIVE & RUNNING${NC}"
    else
        echo -e "  [✘] Service ${BOLD}${srv}${NC}: ${RED}FAILED / INACTIVE${NC}"
    fi
}

check_service "mariadb"
check_service "freeradius"
check_service "accel-ppp"
check_service "radius-server"

echo ""
echo -e "${GREEN}${BOLD}===================================================================${NC}"
echo -e "${GREEN}${BOLD}      🎉 HAITHAM RADIUS ENTERPRISE INSTALLED SUCCESSFULLY!         ${NC}"
echo -e "${GREEN}${BOLD}===================================================================${NC}"
echo ""
echo -e "${BOLD}📌 Web Control-Plane Dashboard:${NC}"
echo -e "   HTTPS Portal:   ${CYAN}https://${SERVER_IP}${NC} (Port 443)"
echo -e "   HTTP Redirect:  ${CYAN}http://${SERVER_IP}${NC} (Port 80 -> 443)"
echo -e "   Legacy Port:    ${CYAN}http://${SERVER_IP}:5000${NC} (Port 5000 -> 443)"
echo -e "   Credentials:    ${YELLOW}Stored securely in ${ENV_FILE}${NC} (chmod 0600)"
echo -e "   Inspect:        ${YELLOW}sudo cat ${ENV_FILE}${NC}"
echo ""
echo -e "${BOLD}🔑 FreeRADIUS Engine Details:${NC}"
echo -e "   Auth Port:      ${CYAN}1812 UDP${NC}"
echo -e "   Acct Port:      ${CYAN}1813 UDP${NC}"
echo -e "   CoA/PoD Port:   ${CYAN}3799 UDP${NC}"
echo -e "   Database:       ${CYAN}MariaDB (Credentials in ${ENV_FILE})${NC}"
echo ""
echo -e "${BOLD}🌐 Accel-PPP SSTP VPN Tunnel:${NC}"
echo -e "   VPN Port:       ${CYAN}4444 TCP${NC}"
echo -e "   Gateway IP:     ${CYAN}10.171.171.1${NC}"
echo -e "   VPN Pool:       ${CYAN}10.171.171.2-255${NC}"
echo ""
echo -e "${BOLD}📡 MikroTik Expired Redirection URLs:${NC}"
echo -e "   Quota Expired (Notice):   ${CYAN}http://${SERVER_IP}/expired/quota${NC}"
echo -e "   Time Expired (Notice):    ${CYAN}http://${SERVER_IP}/expired/time${NC}"
echo -e "   Quota + Smart Loan:       ${CYAN}http://${SERVER_IP}/expired/quota-loan${NC}"
echo -e "   Time + Smart Loan:        ${CYAN}http://${SERVER_IP}/expired/time-loan${NC}"
echo ""
echo -e "${BOLD}⚙️ Management Commands:${NC}"
echo -e "   Restart Web App:    ${CYAN}systemctl restart radius-server${NC}"
echo -e "   Restart RADIUS:     ${CYAN}systemctl restart freeradius${NC}"
echo -e "   Restart SSTP VPN:   ${CYAN}systemctl restart accel-ppp${NC}"
echo -e "   View Web App Logs:  ${CYAN}journalctl -u radius-server -f${NC}"
echo ""
echo -e "${GREEN}===================================================================${NC}"
