CREATE DATABASE IF NOT EXISTS radiusdb CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'radius'@'localhost' IDENTIFIED BY 'radiuspass';
GRANT ALL PRIVILEGES ON radiusdb.* TO 'radius'@'localhost';
CREATE USER IF NOT EXISTS 'radius'@'127.0.0.1' IDENTIFIED BY 'radiuspass';
GRANT ALL PRIVILEGES ON radiusdb.* TO 'radius'@'127.0.0.1';
FLUSH PRIVILEGES;
