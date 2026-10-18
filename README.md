# Haitham RADIUS Server (سيرفر راديوس مايكروتك فائق الأداء)

[![Go Version](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat&logo=go)](https://golang.org)
[![MikroTik RouterOS](https://img.shields.io/badge/MikroTik-RouterOS_v6%20%7C%20v7-black?style=flat)](https://mikrotik.com)
[![Scale](https://img.shields.io/badge/Subscribers-200%2C000%2B-brightgreen?style=flat)]()

سيرفر راديوس عالي الأداء والأمان مخصص ومطور خصيصاً لراوترات وسيرفرات **مايكروتك (MikroTik RouterOS)**، مكتوب بالكامل بلغة **Go (Golang)**، ومزود بواجهة تحكم ويب عصرية مدمجة، وذاكرة كاش فائقة السرعة قادرة على خدمة أكثر من **200,000 مشترك** في نفس الوقت بزمن استجابة أقل من 0.1 مللي ثانية!

---

## 🚀 المميزات الرئيسية (Key Features)

1. **دعم كامل لبروتوكولات مايكروتك (PPPoE & Hotspot):**
   - يدعم مصادقة برودباند **PPPoE** و **Hotspot** و **VPN (OVPN/L2TP/SSTP)**.
   - يدعم بروتوكولات التشفير: **PAP**, **CHAP (MD5)**, **MS-CHAPv2**.

2. **دعم سمات مايكروتك الخاصة (MikroTik VSAs - Vendor ID: 14988):**
   - `Mikrotik-Rate-Limit` لتحديد سرعات الرفع والتحميل تلقائياً (مثل `5M/10M` أو `2048k/4096k`).
   - `Mikrotik-Address-List` لإضافة المشتركين تلقائياً في قوائم العناوين.
   - `Framed-IP-Address` لتثبيت آيبي ثابت للمشترك.
   - `Session-Timeout` و `Idle-Timeout`.
   - `Acct-Interim-Interval` لتحديث استهلاك العميل دورياً.

3. **قطع الاتصال وتعديل السرعة فورياً (CoA & PoD - RFC 3576 / RFC 5176):**
   - إرسال أوامر Disconnect-Request على بورت `3799 UDP` لفصل العميل فور انتهاء باقته أو بضغطة زر من لوحة التحكم دون الحاجة للدخول للراوتر.

4. **محرك تخزين وكاش فائق السرعة يدعم +200,000 مستخدم:**
   - تخزين مستمر بنمط **SQLite WAL Mode** مع ذاكرة مدمجة `In-Memory Thread-Safe Cache`.
   - استعلامات المصادقة تتم في نانو-ثانية (Nanoseconds) من الذاكرة العشوائية لتجنب أي ضغط على الهارد ديسك أثناء ذروة دخول آلاف المشتركين في نفس الثانية.

5. **واجهة تحكم ويب عصرية مدمجة (Embedded Web SPA):**
   - واجهة عربية سريعة وخفيفة جداً تستهلك أقل من 10 ميجابايت رام.
   - إحصائيات مباشرة لحركة المرور، عدد المتصلين الآن، إجمالي المشتركين، وحجم البيانات.
   - إدارة كاملة للمشتركين (إضافة، تعديل، حذف، بحث لحظي، فلترة).
   - مولد كروت دفعة واحدة (Batch Generator) لطباعة وتوليد آلاف كروت الهوتسبوت في ثوانٍ.
   - كود إعداد تلقائي لمايكروتك (MikroTik Auto-Script) بضغطة زر واحدة.

---

## 🛠️ المنافذ المستخدمة (Ports)

- `1812 UDP`: مصادقة الدخول (Authentication - Access-Request)
- `1813 UDP`: سجلات المحاسبة والاستهلاك (Accounting - Start/Interim/Stop)
- `3799 UDP`: قطع الاتصال وإرسال الأوامر (CoA / Packet of Disconnect)
- `8080 TCP`: لوحة تحكم الويب والـ REST API

---

## ⚡ التشغيل السريع (Quick Start)

### 1. البناء والتشغيل محلياً:
```bash
# تشغيل السيرفر
go run ./cmd/server
```

### 2. البناء لنظام لينكس سيرفر:
```bash
GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/radius-server ./cmd/server
```

### 3. ربط راوتر المايكروتك بالسيرفر:
افتح **Terminal** في المايكروتك والصق الأمر التالي (مع استبدال الآيبي بآيبي السيرفر):
```routeros
/ppp aaa set use-radius=yes interim-update=2m accounting=yes
/radius incoming set accept=yes port=3799
/radius add address=192.168.2.90 secret="radius123" service=ppp,hotspot authentication-port=1812 accounting-port=1813 timeout=3s comment="Radius-Server-By-Haitham"
```

---

## 👨‍💻 المطور
تم التطوير بواسطة **Haitham** لنظم الشبكات وإدارة مزودي الخدمة وتطبيقات المايكروتك.
