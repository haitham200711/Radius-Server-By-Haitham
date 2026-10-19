package sysinfo

import (
	"fmt"
)

// FormatBytes formats a byte count into a human-readable string (KB, MB, GB).
func FormatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// FormatUptime formats seconds into an Arabic human-readable string.
func FormatUptime(secs int64) string {
	if secs <= 0 {
		return "لحظات"
	}
	days := secs / 86400
	hours := (secs % 86400) / 3600
	mins := (secs % 3600) / 60

	if days > 0 {
		return fmt.Sprintf("%d يوم و %d ساعة و %d دقيقة", days, hours, mins)
	}
	if hours > 0 {
		return fmt.Sprintf("%d ساعة و %d دقيقة", hours, mins)
	}
	return fmt.Sprintf("%d دقيقة", mins)
}
