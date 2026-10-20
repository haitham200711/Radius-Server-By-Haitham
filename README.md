# Haitham RADIUS Enterprise Platform (نظام راديوس مايكروتك الشامل)

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://golang.org)
[![FreeRADIUS](https://img.shields.io/badge/FreeRADIUS-v3-blue?style=flat)](https://freeradius.org)
[![MariaDB](https://img.shields.io/badge/MariaDB-10.6+-brown?style=flat&logo=mariadb)](https://mariadb.org)
[![MikroTik RouterOS](https://img.shields.io/badge/MikroTik-RouterOS_v6%20%7C%20v7-black?style=flat)](https://mikrotik.com)
[![Scale](https://img.shields.io/badge/Subscribers-200%2C000%2B-brightgreen?style=flat)]()

نظام راديوس مايكروتك مؤسسي ضخم يجمع بين محرك **FreeRADIUS v3** القياسي المعتمد عالمياً، وقاعدة بيانات **MariaDB / MySQL**، مع منصة تحكم متكاملة مبرمجة بلغة **Go** بتصميم وهيكل موديولات احترافي (Blueprints Architecture) مستوحى من نظام `mikrotik_cards_system`، ويدعم نظام صلاحيات متعدد للأدمن والموزعين (Admins & Resellers) وطباعة كروت الشحن وشيتات A4.

---

## ⚡ التثبيت بضغطة زر واحدة (One-Click Automated Install)

يدعم النظام التثبيت التلقائي بالكامل على أنظمة **Ubuntu 20.04 / 22.04 / 24.04 LTS** و **Debian 11 / 12** عبر سكريبت واحد يقوم بتثبيت وضبط جميع المكونات (MariaDB, FreeRADIUS, Accel-PPP SSTP, Go Platform):

### الطريقة الأولى: أمر مباشر من GitHub
قم بفتح الطرفية (Terminal) على سيرفر اللينكس كـ `root` ونفّذ الأمر التالي:

```bash
curl -fsSL https://raw.githubusercontent.com/haitham200711/Radius-Server-By-Haitham/master/install.sh | sudo bash
```

### الطريقة الثانية: عبر استنساخ المستودع (Git Clone)
```bash
git clone https://github.com/haitham200711/Radius-Server-By-Haitham.git /opt/radius-server
cd /opt/radius-server
sudo bash install.sh
```

---

## 🚀 بيانات الدخول الافتراضية
* **رابط لوحة التحكم (HTTPS):** `https://<SERVER_IP>` (أو البورت `443`)
* **تحويل تلقائي:** `http://<SERVER_IP>` (البورت `80` و `5000` يتم تحويلهما تلقائياً إلى `HTTPS`)
* **اسم المستخدم:** `admin`
* **كلمة المرور:** `admin123`

---

## 🔌 البورتات والخدمات المستخدمة (Default Ports)
| الخدمة | البورت / البروتوكول | الوصف |
| :--- | :--- | :--- |
| **لوحة التحكم المشفرة (HTTPS)** | `443 TCP` | Web Control-Plane UI (SSL/TLS) |
| **تحويل الويب التلقائي (HTTP)** | `80 TCP` | HTTP to HTTPS Redirect + MikroTik Script Fetch |
| **البورت القديم (Legacy Port)** | `5000 TCP` | Port 5000 to HTTPS 443 Redirect |
| **مصادقة الراديوس (Auth)** | `1812 UDP` | FreeRADIUS Authentication |
| **محاسبة الراديوس (Acct)** | `1813 UDP` | FreeRADIUS Accounting |
| **أوامر الفصل والتعديل (CoA)**| `3799 UDP` | Disconnect / Change of Auth |
| **سيرفر VPN SSTP** | `4444 TCP` | Accel-PPP SSTP Server |
| **قاعدة البيانات** | `3306 TCP (Local)` | MariaDB (radius:radiuspass@radiusdb) |
