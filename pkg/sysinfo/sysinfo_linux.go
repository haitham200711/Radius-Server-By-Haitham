//go:build linux

package sysinfo

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

var (
	cpuMu          sync.Mutex
	lastCPUTotal   uint64
	lastCPUIdle    uint64
	lastSampleTime time.Time
	lastCPUUsage   float64
)

// GetResources gathers real-time hardware metrics on Linux systems.
func GetResources() *SystemResources {
	host, _ := os.Hostname()
	osName, kernel := getOSInfo()
	uptimeSecs, uptimeFormatted := getUptime()
	cpuModel := getCPUModel()
	cores := runtime.NumCPU()
	cpuUsage := calculateCPUUsage()
	loadAvg := getLoadAverage()

	memTotal, memUsed, memFree, memUsage, memFormatted := getMemoryInfo()
	diskTotal, diskUsed, diskFree, diskUsage, diskFormatted := getDiskInfo()

	return &SystemResources{
		Hostname:       host,
		OS:             osName,
		Kernel:         kernel,
		Uptime:         uptimeFormatted,
		UptimeSeconds:  uptimeSecs,
		CPUModel:       cpuModel,
		CPUCores:       cores,
		CPUUsage:       cpuUsage,
		LoadAverage:    loadAvg,
		MemTotalBytes:     memTotal,
		MemUsedBytes:      memUsed,
		MemFreeBytes:      memFree,
		MemFreeFormatted:  FormatBytes(memFree),
		MemUsage:          memUsage,
		MemFormatted:      memFormatted,
		DiskTotalBytes:    diskTotal,
		DiskUsedBytes:     diskUsed,
		DiskFreeBytes:     diskFree,
		DiskFreeFormatted: FormatBytes(diskFree),
		DiskUsage:         diskUsage,
		DiskFormatted:     diskFormatted,
		ServerTime:        time.Now().Format("2006-01-02 15:04:05"),
	}
}

func getCPUStats() (uint64, uint64, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "cpu ") {
			fields := strings.Fields(line)
			if len(fields) >= 5 {
				var total, idle uint64
				for i := 1; i < len(fields); i++ {
					val, _ := strconv.ParseUint(fields[i], 10, 64)
					total += val
					if i == 4 || i == 5 { // idle and iowait
						idle += val
					}
				}
				return total, idle, nil
			}
		}
	}
	return 0, 0, fmt.Errorf("cpu line not found")
}

func calculateCPUUsage() float64 {
	cpuMu.Lock()
	defer cpuMu.Unlock()

	total, idle, err := getCPUStats()
	if err != nil {
		return lastCPUUsage
	}

	if lastCPUTotal == 0 {
		lastCPUTotal = total
		lastCPUIdle = idle
		lastSampleTime = time.Now()
		// Brief sleep for first accurate calculation
		time.Sleep(100 * time.Millisecond)
		total2, idle2, err2 := getCPUStats()
		if err2 == nil && total2 > total {
			deltaTotal := float64(total2 - total)
			deltaIdle := float64(idle2 - idle)
			usage := 100.0 * (deltaTotal - deltaIdle) / deltaTotal
			lastCPUTotal = total2
			lastCPUIdle = idle2
			lastSampleTime = time.Now()
			lastCPUUsage = math.Round(math.Max(0.0, math.Min(100.0, usage))*10) / 10
			return lastCPUUsage
		}
		return 0.0
	}

	deltaTotal := float64(total - lastCPUTotal)
	deltaIdle := float64(idle - lastCPUIdle)

	if deltaTotal > 0 {
		usage := 100.0 * (deltaTotal - deltaIdle) / deltaTotal
		lastCPUUsage = math.Round(math.Max(0.0, math.Min(100.0, usage))*10) / 10
		lastCPUTotal = total
		lastCPUIdle = idle
		lastSampleTime = time.Now()
	}

	return lastCPUUsage
}

func getMemoryInfo() (uint64, uint64, uint64, float64, string) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0, 0, 0, ""
	}
	defer file.Close()

	var memTotal, memFree, memAvailable, buffers, cached uint64
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) >= 2 {
			val, _ := strconv.ParseUint(parts[1], 10, 64)
			switch parts[0] {
			case "MemTotal:":
				memTotal = val * 1024
			case "MemFree:":
				memFree = val * 1024
			case "MemAvailable:":
				memAvailable = val * 1024
			case "Buffers:":
				buffers = val * 1024
			case "Cached:":
				cached = val * 1024
			}
		}
	}

	if memAvailable == 0 {
		memAvailable = memFree + buffers + cached
	}

	used := uint64(0)
	if memTotal > memAvailable {
		used = memTotal - memAvailable
	}
	free := memAvailable

	usagePct := 0.0
	if memTotal > 0 {
		usagePct = math.Round((float64(used)/float64(memTotal)*100.0)*10) / 10
	}

	formatted := fmt.Sprintf("%s / %s", FormatBytes(used), FormatBytes(memTotal))
	return memTotal, used, free, usagePct, formatted
}

func getDiskInfo() (uint64, uint64, uint64, float64, string) {
	var stat unix.Statfs_t
	if err := unix.Statfs("/", &stat); err != nil {
		return 0, 0, 0, 0, ""
	}

	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	used := uint64(0)
	if total > free {
		used = total - free
	}

	usagePct := 0.0
	if total > 0 {
		usagePct = math.Round((float64(used)/float64(total)*100.0)*10) / 10
	}

	formatted := fmt.Sprintf("%s / %s", FormatBytes(used), FormatBytes(total))
	return total, used, free, usagePct, formatted
}

func getUptime() (int64, string) {
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		return 0, "غير متاح"
	}
	fields := strings.Fields(string(data))
	if len(fields) > 0 {
		secs, _ := strconv.ParseFloat(fields[0], 64)
		s := int64(secs)
		return s, FormatUptime(s)
	}
	return 0, "غير متاح"
}

func getCPUModel() string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return "Generic CPU"
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "Generic CPU"
}

func getOSInfo() (string, string) {
	osName := "Linux"
	kernel := ""

	if kData, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		kernel = strings.TrimSpace(string(kData))
	}

	if data, err := os.ReadFile("/etc/os-release"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "PRETTY_NAME=") {
				osName = strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), "\"")
				break
			}
		}
	}

	return osName, kernel
}

func getLoadAverage() string {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return "0.00, 0.00, 0.00"
	}
	fields := strings.Fields(string(data))
	if len(fields) >= 3 {
		return fmt.Sprintf("%s, %s, %s", fields[0], fields[1], fields[2])
	}
	return "0.00, 0.00, 0.00"
}
