package i18n

import "strings"

var translations = map[string]map[string]string{
	// Brand & Common
	"راديوس مايكروتك PRO": {
		"ar": "راديوس مايكروتك PRO",
		"en": "MikroTik RADIUS PRO",
	},
	"نظام إدارة الشبكات والمشتركين السحابي": {
		"ar": "نظام إدارة الشبكات والمشتركين السحابي",
		"en": "Cloud Network & Subscriber Management",
	},
	"الرئيسية": {
		"ar": "الرئيسية",
		"en": "Dashboard",
	},
	"المشتركين": {
		"ar": "المشتركين",
		"en": "Subscribers",
	},
	"قائمة المشتركين": {
		"ar": "قائمة المشتركين",
		"en": "Subscribers List",
	},
	"المشتركين المتصلين": {
		"ar": "المشتركين المتصلين",
		"en": "Online Subscribers",
	},
	"سجل تسجيل الدخول": {
		"ar": "سجل تسجيل الدخول",
		"en": "Login Logs",
	},
	"العروض": {
		"ar": "العروض",
		"en": "Offers & Speed",
	},
	"البروفايلات": {
		"ar": "البروفايلات",
		"en": "Profiles",
	},
	"التحكم بالسرعات": {
		"ar": "التحكم بالسرعات",
		"en": "Speed Control",
	},
	"كروت": {
		"ar": "كروت",
		"en": "Cards",
	},
	"كروت الشحن": {
		"ar": "كروت الشحن",
		"en": "Recharge Cards",
	},
	"كروت الكافيهات": {
		"ar": "كروت الكافيهات",
		"en": "Cafe Hotspot Cards",
	},
	"اشكال الكروت": {
		"ar": "اشكال الكروت",
		"en": "Card Templates",
	},
	"NAS": {
		"ar": "NAS",
		"en": "NAS Routers",
	},
	"SSTP Server": {
		"ar": "SSTP Server",
		"en": "SSTP Server",
	},
	"التقارير": {
		"ar": "التقارير",
		"en": "Reports",
	},
	"الواتساب": {
		"ar": "الواتساب",
		"en": "WhatsApp Bot",
	},
	"الموزعين": {
		"ar": "الموزعين",
		"en": "Resellers",
	},
	"الإعدادات": {
		"ar": "الإعدادات",
		"en": "Settings",
	},
	"إعدادات النظام والأمان": {
		"ar": "إعدادات النظام والأمان",
		"en": "System & Security",
	},
	"صفحة المشترك": {
		"ar": "صفحة المشترك",
		"en": "Subscriber Portal",
	},
	"النسخ الاحتياطي": {
		"ar": "النسخ الاحتياطي",
		"en": "Backups",
	},
	"استيراد ADV&SAS": {
		"ar": "استيراد ADV&SAS",
		"en": "Import ADV&SAS",
	},
	"صيانة النظام والخدمات": {
		"ar": "صيانة النظام والخدمات",
		"en": "System Maintenance",
	},
	"تسجيل الخروج": {
		"ar": "تسجيل الخروج",
		"en": "Logout",
	},

	// Header & Status
	"خادم SSTP VPN نشط": {
		"ar": "خادم SSTP VPN نشط",
		"en": "SSTP VPN Server Active",
	},
	"بوت الواتساب": {
		"ar": "بوت الواتساب",
		"en": "WhatsApp Bot",
	},
	"مدير النظام": {
		"ar": "مدير النظام",
		"en": "Administrator",
	},
	"موزع معتمد": {
		"ar": "موزع معتمد",
		"en": "Reseller",
	},
	"صلاحيات الإدارة الكاملة": {
		"ar": "صلاحيات الإدارة الكاملة",
		"en": "Full Admin Access",
	},
	"حساب موزع معتمد": {
		"ar": "حساب موزع معتمد",
		"en": "Authorized Reseller Account",
	},
	"الداشبورد والمتابعة": {
		"ar": "الداشبورد والمتابعة",
		"en": "Dashboard & Stats",
	},
	"إعدادات النظام والربط": {
		"ar": "إعدادات النظام والربط",
		"en": "System Settings",
	},
	"سجلات المحاسبة": {
		"ar": "سجلات المحاسبة",
		"en": "Accounting Logs",
	},
	"تغيير كلمة المرور": {
		"ar": "تغيير كلمة المرور",
		"en": "Change Password",
	},

	// Common UI Actions
	"بحث": {
		"ar": "بحث",
		"en": "Search",
	},
	"إضافة": {
		"ar": "إضافة",
		"en": "Add",
	},
	"تعديل": {
		"ar": "تعديل",
		"en": "Edit",
	},
	"حذف": {
		"ar": "حذف",
		"en": "Delete",
	},
	"حفظ": {
		"ar": "حفظ",
		"en": "Save",
	},
	"إلغاء": {
		"ar": "إلغاء",
		"en": "Cancel",
	},
	"طباعة": {
		"ar": "طباعة",
		"en": "Print",
	},
	"تصدير": {
		"ar": "تصدير",
		"en": "Export",
	},
	"تجديد": {
		"ar": "تجديد",
		"en": "Renew",
	},
	"سداد": {
		"ar": "سداد",
		"en": "Pay",
	},
	"الإجراءات": {
		"ar": "الإجراءات",
		"en": "Actions",
	},
	"الحالة": {
		"ar": "الحالة",
		"en": "Status",
	},
	"الاسم": {
		"ar": "الاسم",
		"en": "Name",
	},
	"اسم المستخدم": {
		"ar": "اسم المستخدم",
		"en": "Username",
	},
	"كلمة المرور": {
		"ar": "كلمة المرور",
		"en": "Password",
	},
	"الهاتف": {
		"ar": "الهاتف",
		"en": "Phone",
	},
	"الرصيد": {
		"ar": "الرصيد",
		"en": "Balance",
	},
	"الرصيد المتاح": {
		"ar": "الرصيد المتاح",
		"en": "Available Balance",
	},
	"رصيد المحفظة": {
		"ar": "رصيد المحفظة",
		"en": "Wallet Balance",
	},
	"المديونية": {
		"ar": "المديونية",
		"en": "Debt",
	},
	"شحن رصيد": {
		"ar": "شحن رصيد",
		"en": "Add Balance",
	},
	"شحن / إيداع رصيد بالمحفظة": {
		"ar": "شحن / إيداع رصيد بالمحفظة",
		"en": "Deposit to Wallet",
	},
	"سداد / تسوية مديونية": {
		"ar": "سداد / تسوية مديونية",
		"en": "Pay / Settle Debt",
	},
	"الكوتة": {
		"ar": "الكوتة",
		"en": "Quota",
	},
	"السرعة": {
		"ar": "السرعة",
		"en": "Speed",
	},
	"السعر": {
		"ar": "السعر",
		"en": "Price",
	},
	"تاريخ البدء": {
		"ar": "تاريخ البدء",
		"en": "Start Date",
	},
	"تاريخ الانتهاء": {
		"ar": "تاريخ الانتهاء",
		"en": "Expiration Date",
	},
	"المتبقي": {
		"ar": "المتبقي",
		"en": "Remaining",
	},
	"المستهلك": {
		"ar": "المستهلك",
		"en": "Used",
	},
	"نشط": {
		"ar": "نشط",
		"en": "Active",
	},
	"متوقف": {
		"ar": "متوقف",
		"en": "Disabled",
	},
	"منتهي": {
		"ar": "منتهي",
		"en": "Expired",
	},
	"تأكيد": {
		"ar": "تأكيد",
		"en": "Confirm",
	},
	"إغلاق": {
		"ar": "إغلاق",
		"en": "Close",
	},
	"ريستارت": {
		"ar": "ريستارت",
		"en": "Restart",
	},
	"فصل": {
		"ar": "فصل",
		"en": "Disconnect",
	},
}

// T translates a key based on lang ("ar" or "en"). If not found, returns key.
func T(lang, key string) string {
	trimmed := strings.TrimSpace(key)
	if entry, ok := translations[trimmed]; ok {
		if val, exists := entry[lang]; exists && val != "" {
			return val
		}
	}
	return key
}

// TR returns enText if lang == "en", otherwise arText.
func TR(lang, arText, enText string) string {
	if strings.ToLower(strings.TrimSpace(lang)) == "en" {
		return enText
	}
	return arText
}

// Direction returns "ltr" if lang == "en", else "rtl"
func Direction(lang string) string {
	if strings.ToLower(strings.TrimSpace(lang)) == "en" {
		return "ltr"
	}
	return "rtl"
}
