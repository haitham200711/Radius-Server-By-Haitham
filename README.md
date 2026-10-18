# Haitham RADIUS Enterprise Platform (نظام راديوس مايكروتك الشامل)

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://golang.org)
[![FreeRADIUS](https://img.shields.io/badge/FreeRADIUS-v3-blue?style=flat)](https://freeradius.org)
[![MariaDB](https://img.shields.io/badge/MariaDB-10.6+-brown?style=flat&logo=mariadb)](https://mariadb.org)
[![MikroTik RouterOS](https://img.shields.io/badge/MikroTik-RouterOS_v6%20%7C%20v7-black?style=flat)](https://mikrotik.com)
[![Scale](https://img.shields.io/badge/Subscribers-200%2C000%2B-brightgreen?style=flat)]()

نظام راديوس مايكروتك مؤسسي ضخم يجمع بين محرك **FreeRADIUS v3** القياسي المعتمد عالمياً، وقاعدة بيانات **MariaDB / MySQL**، مع منصة تحكم متكاملة مبرمجة بلغة **Go** بتصميم وهيكل موديولات احترافي (Blueprints Architecture) مستوحى من نظام `mikrotik_cards_system`، ويدعم نظام صلاحيات متعدد للأدمن والموزعين (Admins & Resellers) وطباعة كروت الشحن وشيتات A4.

---

## 🏛️ المعمارية التقنية (Architecture)

1. **FreeRADIUS v3 Server:**
   * استقبال ومعالجة حزم الـ UDP على البورتات القياسية:
     * `1812 UDP`: مصادقة المشتركين (Authentication)
     * `1813 UDP`: سجلات المحاسبة وتتبع الاستهلاك (Accounting)
     * `3799 UDP`: استقبال وإرسال أوامر الفصل وتعديل السرعة (CoA / PoD)
   * قواميس المايكروتك الرسمية (`Mikrotik-Rate-Limit`, `Framed-IP-Address`, وغيرها).
   * دعم جميع بروتوكولات التشفير: **PAP**, **CHAP**, **MS-CHAPv2**.

2. **MariaDB Database (FreeRADIUS Schema):**
   * جداول الراديوس القياسية: `radcheck`, `radreply`, `radusergroup`, `radgroupcheck`, `radgroupreply`, `radacct`, `nas`.
   * جداول المنصة المؤسسية: `admins`, `resellers`, `profiles`, `batches`, `cards`, `card_templates`.

3. **Go Control-Plane Platform (Blueprints):**
   * `auth`: تسجيل الدخول والخروج والتحقق من الصلاحيات (Admin / Reseller).
   * `dashboard`: لوحة الإحصائيات العامة ومراقبة حركة البيانات.
   * `subscribers`: إدارة مشتركي البرودباند والهوتسبوت في FreeRADIUS.
   * `cards`: توليد الكروت بكميات ضخمة وتجهيزها للطباعة.
   * `card_templates`: تصاميم وأشكال الكروت والباركود.
   * `profiles`: باقات السرعات والكوتة وربطها التلقائي مع `Mikrotik-Rate-Limit`.
   * `routers`: إدارة راوترات المايكروتك NAS.
   * `resellers`: إدارة حسابات الموزعين وأرصدتهم وحدودهم الائتمانية.
   * `sessions`: متابعة الجلسات الحية وفصل العملاء فورياً عبر الـ CoA.
   * `reports`: سجلات المحاسبة التاريخية.
   * `settings`: كود ربط المايكروتك السريع وحالة السيرفر.

---

## 🚀 بيانات الدخول الافتراضية
* **الرابط:** `http://192.168.2.90:5000`
* **المستخدم الافتراضي:** `admin`
* **كلمة المرور:** `admin123`
