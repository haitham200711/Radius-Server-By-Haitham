#!/bin/bash
DB_PASS="${DB_PASS:-}"
if [[ -z "$DB_PASS" && -f "/opt/radius-server/.env" ]]; then
    DB_PASS=$(grep "DB_DSN=" /opt/radius-server/.env | sed -E 's/.*:([^@]+)@.*/\1/' | head -n1)
fi
mariadb < /tmp/init_mariadb.sql
mariadb -u radius -p"${DB_PASS}" radiusdb < /etc/freeradius/3.0/mods-config/sql/main/mysql/schema.sql
echo "FREERADIUS SCHEMA APPLIED SUCCESSFULLY"
mariadb -u radius -p"${DB_PASS}" -e "SHOW TABLES IN radiusdb;"
