#!/bin/bash
mariadb < /tmp/init_mariadb.sql
mariadb -u radius -pradiuspass radiusdb < /etc/freeradius/3.0/mods-config/sql/main/mysql/schema.sql
echo "FREERADIUS SCHEMA APPLIED SUCCESSFULLY"
mariadb -u radius -pradiuspass -e "SHOW TABLES IN radiusdb;"
