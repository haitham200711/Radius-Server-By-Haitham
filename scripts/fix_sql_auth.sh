#!/bin/bash
set -e

SQL_CONF="/etc/freeradius/3.0/mods-available/sql"

# Uncomment and set server, port, login, password, radius_db
sed -i 's/^[[:space:]]*#[[:space:]]*server = "localhost"/server = "127.0.0.1"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*#[[:space:]]*port = 3306/port = 3306/' "$SQL_CONF"
sed -i 's/^[[:space:]]*#[[:space:]]*login = "radius"/login = "radius"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*#[[:space:]]*password = "radpass"/password = "radiuspass"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*#[[:space:]]*radius_db = "radius"/radius_db = "radiusdb"/' "$SQL_CONF"

# In case they were already uncommented or different
sed -i 's/^[[:space:]]*server = "localhost"/server = "127.0.0.1"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*login = "radius"/login = "radius"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*password = "radpass"/password = "radiuspass"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*radius_db = "radius"/radius_db = "radiusdb"/' "$SQL_CONF"

# Uncomment read_clients and client_table
sed -i 's/^[[:space:]]*#[[:space:]]*read_clients = yes/read_clients = yes/' "$SQL_CONF"
sed -i 's/^[[:space:]]*#[[:space:]]*client_table = "nas"/client_table = "nas"/' "$SQL_CONF"

echo "=== SQL CONFIG UPDATED ==="
grep -n -E 'server =|port =|login =|password =|radius_db =' "$SQL_CONF" | head -n 10

systemctl restart freeradius
systemctl status freeradius --no-pager
