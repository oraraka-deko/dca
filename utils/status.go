package utils


import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/process"
) //"encoding/json"

// Status represents the system status information
type Status struct {
	// CPU Information
	CPUPercent  float64   `json:"cpupercent"`
	CPUCount    int       `json:"cpucount"`
	CPUModel    string    `json:"cputype"`
	CPULoad1    float64   `json:"cpuload1"`
	CPULoad5    float64   `json:"cpuload5"`
	CPULoad15   float64   `json:"cpuload15"`
	
	// Memory Information
	MemCurrent      uint64  `json:"memcurrent"`
	MemTotal        uint64  `json:"memtotal"`
	MemUsedPercent  float64 `json:"memusedpercent"`
	
	// Swap Information
	SwapCurrent     uint64  `json:"swapcurrent"`
	SwapTotal       uint64  `json:"swaptotal"`
	
	// Disk Information
	DiskCurrent      uint64  `json:"diskcurrent"`
	DiskTotal        uint64  `json:"disktotal"`
	DiskUsedPercent  float64 `json:"diskusedpercent"`
	
	// Disk I/O Information
	IORead        uint64 `json:"ioread"`
	IOWrite       uint64 `json:"iowrite"`
	IOReadCount   uint64 `json:"ioreadcount"`
	IOWriteCount  uint64 `json:"iowritecount"`
	
	// Network Information
	NetSent  uint64 `json:"netsent"`
	NetRec   uint64 `json:"netrec"`
	NetPSent uint64 `json:"netpsent"`
	NetPRec  uint64 `json:"netprec"`
	NetErrIn uint64 `json:"neterrin"`
	NetErrOut uint64 `json:"neterrout"`
	NetDropIn uint64 `json:"netdropin"`
	NetDropOut uint64 `json:"netdropout"`
	
	// Application Information
	AppMem       uint64 `json:"appmem"`
	AppThreads   uint32 `json:"appthreads"`
	AppGoRoutines int   `json:"appgoroutines"`
	
	// Host Information
	Hostname       string   `json:"hostname"`
	IPv4           []string `json:"ipv4"`
	IPv6           []string `json:"ipv6"`
	BootTime       uint64   `json:"boottime"`
	Uptime         uint64   `json:"uptime"`
	OS             string   `json:"os"`
	Platform       string   `json:"platform"`
	PlatformFamily string   `json:"platformfamily"`
	PlatformVersion string  `json:"platformversion"`
	KernelVersion  string   `json:"kernelversion"`
	KernelArch     string   `json:"kernelarch"`
	HostID         string   `json:"hostid"`
	
	// Process Information
	ProcessCount int `json:"processcount"`
	
	// Additional Information
	Error     string `json:"error,omitempty"`
	Timestamp int64  `json:"timestamp"`
}
// //export FreeString
// func FreeString(s *C.char) {
// 	C.free(unsafe.Pointer(s))
// }
// //export GetStatus
// func GetStatus() *C.char {
// 	status := GetStatusInfoJSON()
//    jsonData, _ := json.Marshal(status)
//    fs := string(jsonData)
// 	return C.CString(fs)
// }
func GetStatusInfoJSON() Status {
	status := Status{
		Timestamp: time.Now().Unix(),
	}
	
	var errors []string

	// Get CPU information
	cpuInfo, err := cpu.Info()
	if err != nil {
		fmt.Println("get cpu info failed:", err)
		errors = append(errors, "cpu_info: "+err.Error())
	} else if len(cpuInfo) > 0 {
		status.CPUModel = cpuInfo[0].ModelName
	}
	status.CPUCount = runtime.NumCPU()

	// Get CPU usage (non-blocking by passing 0 interval)
	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		fmt.Println("get cpu percent failed:", err)
		errors = append(errors, "cpu_percent: "+err.Error())
	} else if len(cpuPercent) > 0 {
		status.CPUPercent = cpuPercent[0]
	}

	// Get CPU load averages
	loadAvg, err := load.Avg()
	if err != nil {
		fmt.Println("get cpu load failed:", err)
		errors = append(errors, "cpu_load: "+err.Error())
	} else {
		status.CPULoad1 = loadAvg.Load1
		status.CPULoad5 = loadAvg.Load5
		status.CPULoad15 = loadAvg.Load15
	}

	// Get memory information
	memInfo, err := mem.VirtualMemory()
	if err != nil {
		fmt.Println("get virtual memory failed:", err)
		errors = append(errors, "memory: "+err.Error())
	} else {
		status.MemCurrent = memInfo.Used
		status.MemTotal = memInfo.Total
		status.MemUsedPercent = memInfo.UsedPercent
	}

	// Get swap memory
	swapInfo, err := mem.SwapMemory()
	if err != nil {
		fmt.Println("get swap memory failed:", err)
		errors = append(errors, "swap: "+err.Error())
	} else {
		status.SwapCurrent = swapInfo.Used
		status.SwapTotal = swapInfo.Total
	}

	// Get disk usage
	diskInfo, err := disk.Usage("/")
	if err != nil {
		fmt.Println("get disk usage failed:", err)
		errors = append(errors, "disk: "+err.Error())
	} else {
		status.DiskCurrent = diskInfo.Used
		status.DiskTotal = diskInfo.Total
		status.DiskUsedPercent = diskInfo.UsedPercent
	}

	// Get disk I/O counters
	ioStats, err := disk.IOCounters()
	if err != nil {
		fmt.Println("get disk io counters failed:", err)
		errors = append(errors, "disk_io: "+err.Error())
	} else {
		infoR, infoW := uint64(0), uint64(0)
		infoRCount, infoWCount := uint64(0), uint64(0)
		for _, ioStat := range ioStats {
			infoR += ioStat.ReadBytes
			infoW += ioStat.WriteBytes
			infoRCount += ioStat.ReadCount
			infoWCount += ioStat.WriteCount
		}
		status.IORead = infoR
		status.IOWrite = infoW
		status.IOReadCount = infoRCount
		status.IOWriteCount = infoWCount
	}

	// Get network I/O statistics
	netStats, err := net.IOCounters(false) // false = aggregate all interfaces
	if err != nil {
		fmt.Println("get net io counters failed:", err)
		errors = append(errors, "network: "+err.Error())
	} else if len(netStats) > 0 {
		netStat := netStats[0]
		status.NetSent = netStat.BytesSent
		status.NetRec = netStat.BytesRecv
		status.NetPSent = netStat.PacketsSent
		status.NetPRec = netStat.PacketsRecv
		status.NetErrIn = netStat.Errin
		status.NetErrOut = netStat.Errout
		status.NetDropIn = netStat.Dropin
		status.NetDropOut = netStat.Dropout
	}

	// Get application memory and threads
	var rtm runtime.MemStats
	runtime.ReadMemStats(&rtm)
	status.AppMem = rtm.Sys
	status.AppThreads = uint32(runtime.NumGoroutine())
	status.AppGoRoutines = runtime.NumGoroutine()

	// Get host information
	hostInfo, err := host.Info()
	if err != nil {
		fmt.Println("get host info failed:", err)
		errors = append(errors, "host: "+err.Error())
	} else {
		status.Hostname = hostInfo.Hostname
		status.BootTime = hostInfo.BootTime
		status.Uptime = hostInfo.Uptime
		status.OS = hostInfo.OS
		status.Platform = hostInfo.Platform
		status.PlatformFamily = hostInfo.PlatformFamily
		status.PlatformVersion = hostInfo.PlatformVersion
		status.KernelVersion = hostInfo.KernelVersion
		status.KernelArch = hostInfo.KernelArch
		status.HostID = hostInfo.HostID
	}

	// Get process count
	processes, err := process.Pids()
	if err != nil {
		fmt.Println("get process count failed:", err)
		errors = append(errors, "processes: "+err.Error())
	} else {
		status.ProcessCount = len(processes)
	}

	// Get IP addresses from network interfaces
	ipv4, ipv6 := getIPAddresses()
	status.IPv4 = ipv4
	status.IPv6 = ipv6

	// Collect all errors
	if len(errors) > 0 {
		status.Error = strings.Join(errors, "; ")
	}

	//jsonData, _ := json.Marshal(status)
	return status
}

// getIPAddresses retrieves IPv4 and IPv6 addresses from network interfaces
func getIPAddresses() ([]string, []string) {
	ipv4 := make([]string, 0)
	ipv6 := make([]string, 0)
	
	netInterfaces, err := net.Interfaces()
	if err != nil {
		fmt.Println("get network interfaces failed:", err)
		return ipv4, ipv6
	}
	
	for _, iface := range netInterfaces {
		// Check if interface is up and not loopback
		isUp := false
		isLoopback := false
		for _, flag := range iface.Flags {
			if flag == "up" {
				isUp = true
			}
			if flag == "loopback" {
				isLoopback = true
			}
		}
		
		if isUp && !isLoopback {
			for _, addr := range iface.Addrs {
				addrStr := addr.Addr
				// Remove CIDR notation if present (e.g., "192.168.1.1/24" -> "192.168.1.1")
				if idx := strings.Index(addrStr, "/"); idx != -1 {
					addrStr = addrStr[:idx]
				}
				
				if strings.Contains(addrStr, ".") {
					// IPv4 address
					ipv4 = append(ipv4, addrStr)
				} else if strings.HasPrefix(addrStr, "fe80:") {
					// Skip link-local IPv6 addresses
					continue
				} else {
					// IPv6 address
					ipv6 = append(ipv6, addrStr)
				}
			}
		}
	}
	
	return ipv4, ipv6
}

func GetLogs(count string, level string) []string {
	c, err := strconv.Atoi(count)
	if err != nil {
		c = 10
	}
	return strings.Fields(fmt.Sprintf("%d %s", c, level))
}


func main(){

}