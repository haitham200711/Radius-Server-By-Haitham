package sysinfo

// SystemResources holds real-time hardware and OS metrics of the host machine.
type SystemResources struct {
	Hostname          string  `json:"hostname"`
	OS                string  `json:"os"`
	Kernel            string  `json:"kernel"`
	Uptime            string  `json:"uptime"`
	UptimeSeconds     int64   `json:"uptime_seconds"`
	CPUModel          string  `json:"cpu_model"`
	CPUCores          int     `json:"cpu_cores"`
	CPUUsage          float64 `json:"cpu_usage"`
	LoadAverage       string  `json:"load_average"`
	MemTotalBytes     uint64  `json:"mem_total_bytes"`
	MemUsedBytes      uint64  `json:"mem_used_bytes"`
	MemFreeBytes      uint64  `json:"mem_free_bytes"`
	MemFreeFormatted  string  `json:"mem_free_formatted"`
	MemUsage          float64 `json:"mem_usage"`
	MemFormatted      string  `json:"mem_formatted"`
	DiskTotalBytes    uint64  `json:"disk_total_bytes"`
	DiskUsedBytes     uint64  `json:"disk_used_bytes"`
	DiskFreeBytes     uint64  `json:"disk_free_bytes"`
	DiskFreeFormatted string  `json:"disk_free_formatted"`
	DiskUsage         float64 `json:"disk_usage"`
	DiskFormatted     string  `json:"disk_formatted"`
	ServerTime        string  `json:"server_time"`
}
