#!/bin/bash
set -e

SQL_CONF="/etc/freeradius/3.0/mods-available/sql"

# Configure SQL connection
sed -i 's/^[[:space:]]*dialect = .*/\tdialect = "mysql"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*driver = .*/\tdriver = "rlm_sql_mysql"/' "$SQL_CONF"

# In mysql sub-section
sed -i 's/^[[:space:]]*server = .*/\tserver = "127.0.0.1"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*port = .*/\tport = 3306/' "$SQL_CONF"
sed -i 's/^[[:space:]]*login = .*/\tlogin = "radius"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*password = .*/\tpassword = "radiuspass"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*radius_db = .*/\tradius_db = "radiusdb"/' "$SQL_CONF"
sed -i 's/^[[:space:]]*read_clients = .*/\tread_clients = yes/' "$SQL_CONF"
sed -i 's/^[[:space:]]*client_table = .*/\tclient_table = "nas"/' "$SQL_CONF"

# Comment out TLS parameters in mysql section
sed -i 's/^[[:space:]]*ca_file =/#\tca_file =/' "$SQL_CONF"
sed -i 's/^[[:space:]]*ca_path =/#\tca_path =/' "$SQL_CONF"
sed -i 's/^[[:space:]]*certificate_file =/#\tcertificate_file =/' "$SQL_CONF"
sed -i 's/^[[:space:]]*private_key_file =/#\tprivate_key_file =/' "$SQL_CONF"
sed -i 's/^[[:space:]]*tls_required =/#\ttls_required =/' "$SQL_CONF"

# Enable sql module
ln -sf /etc/freeradius/3.0/mods-available/sql /etc/freeradius/3.0/mods-enabled/sql

# Enable sql in sites-available/default
DEFAULT_SITE="/etc/freeradius/3.0/sites-available/default"
sed -i '/authorize {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
sed -i '/accounting {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
sed -i '/session {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"
sed -i '/post-auth {/,/}/ { s/^[[:space:]]*#[[:space:]]*sql$/\tsql/ }' "$DEFAULT_SITE"

# Fix permissions
chown -h freerad:freerad /etc/freeradius/3.0/mods-enabled/sql
chown -R freerad:freerad /etc/freeradius/3.0/

# Test freeradius configuration
freeradius -C -lstdout

# Restart and enable FreeRADIUS
systemctl restart freeradius
systemctl enable freeradius
systemctl status freeradius --no-pager
