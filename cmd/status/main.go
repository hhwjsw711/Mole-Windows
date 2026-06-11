//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	gonet "net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// Styles
var (
	titleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#C79FD7")).Bold(true)
	headerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#87CEEB")).Bold(true)
	labelStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#888888"))
	valueStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFFFFF"))
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#A5D6A7"))
	warnStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#FFD75F"))
	dangerStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF5F5F")).Bold(true)
	dimStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#666666"))
	cardStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#444444")).Padding(0, 1)
)

// Metrics snapshot
type MetricsSnapshot struct {
	CollectedAt   time.Time
	HealthScore   int
	HealthMessage string

	// Hardware
	Hostname string
	OS       string
	Platform string
	Uptime   time.Duration

	// CPU
	CPUModel   string
	CPUCores   int
	CPUPercent float64
	CPUPerCore []float64

	// Memory
	MemTotal    uint64
	MemUsed     uint64
	MemPercent  float64
	SwapTotal   uint64
	SwapUsed    uint64
	SwapPercent float64

	// Disk
	Disks []DiskInfo

	// Network
	Networks []NetworkInfo

	// Processes
	TopProcesses []ProcessInfo
}

type DiskInfo struct {
	Device      string
	Mountpoint  string
	Total       uint64
	Used        uint64
	Free        uint64
	UsedPercent float64
	Fstype      string
}

type NetworkInfo struct {
	Name        string
	BytesSent   uint64
	BytesRecv   uint64
	PacketsSent uint64
	PacketsRecv uint64
	RxRateMBs   float64
	TxRateMBs   float64
	IP          string
}

type ProcessInfo struct {
	PID         int32
	Name        string
	CPU         float64
	Memory      float32
	Command     string
	MemoryBytes uint64
}

// Collector
type Collector struct {
	prevNet       map[string]net.IOCountersStat
	prevNetTime   time.Time
	prevDisk      map[string]disk.IOCountersStat
	prevDiskTime  time.Time
	mu            sync.Mutex
	procCount     int
	procCountOnce sync.Once
	physicalCores int
	coreCountOnce sync.Once
}

func NewCollector() *Collector {
	return &Collector{
		prevNet:  make(map[string]net.IOCountersStat),
		prevDisk: make(map[string]disk.IOCountersStat),
	}
}

func (c *Collector) Collect() MetricsSnapshot {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var (
		snapshot MetricsSnapshot
		wg       sync.WaitGroup
		mu       sync.Mutex
	)

	snapshot.CollectedAt = time.Now()

	// Host info
	wg.Add(1)
	go func() {
		defer wg.Done()
		if info, err := host.InfoWithContext(ctx); err == nil {
			mu.Lock()
			snapshot.Hostname = info.Hostname
			snapshot.OS = info.OS
			snapshot.Platform = fmt.Sprintf("%s %s", info.Platform, info.PlatformVersion)
			snapshot.Uptime = time.Duration(info.Uptime) * time.Second
			mu.Unlock()
		}
	}()

	// CPU info
	wg.Add(1)
	go func() {
		defer wg.Done()
		if cpuInfo, err := cpu.InfoWithContext(ctx); err == nil && len(cpuInfo) > 0 {
			mu.Lock()
			snapshot.CPUModel = cpuInfo[0].ModelName
			snapshot.CPUCores = runtime.NumCPU()
			mu.Unlock()
		}
		if percent, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, false); err == nil && len(percent) > 0 {
			mu.Lock()
			snapshot.CPUPercent = percent[0]
			mu.Unlock()
		}
		if perCore, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, true); err == nil {
			mu.Lock()
			snapshot.CPUPerCore = perCore
			mu.Unlock()
		}
	}()

	// Memory
	wg.Add(1)
	go func() {
		defer wg.Done()
		if memInfo, err := mem.VirtualMemoryWithContext(ctx); err == nil {
			mu.Lock()
			snapshot.MemTotal = memInfo.Total
			snapshot.MemUsed = memInfo.Used
			snapshot.MemPercent = memInfo.UsedPercent
			mu.Unlock()
		}
		if swapInfo, err := mem.SwapMemoryWithContext(ctx); err == nil {
			mu.Lock()
			snapshot.SwapTotal = swapInfo.Total
			snapshot.SwapUsed = swapInfo.Used
			snapshot.SwapPercent = swapInfo.UsedPercent
			mu.Unlock()
		}
	}()

	// Disk
	wg.Add(1)
	go func() {
		defer wg.Done()
		if partitions, err := disk.PartitionsWithContext(ctx, false); err == nil {
			var disks []DiskInfo
			for _, p := range partitions {
				if !strings.HasPrefix(p.Device, "C:") &&
					!strings.HasPrefix(p.Device, "D:") &&
					!strings.HasPrefix(p.Device, "E:") &&
					!strings.HasPrefix(p.Device, "F:") {
					continue
				}
				if usage, err := disk.UsageWithContext(ctx, p.Mountpoint); err == nil {
					disks = append(disks, DiskInfo{
						Device:      p.Device,
						Mountpoint:  p.Mountpoint,
						Total:       usage.Total,
						Used:        usage.Used,
						Free:        usage.Free,
						UsedPercent: usage.UsedPercent,
						Fstype:      p.Fstype,
					})
				}
			}
			mu.Lock()
			snapshot.Disks = disks
			mu.Unlock()
		}

		c.mu.Lock()
		c.prevDisk = make(map[string]disk.IOCountersStat)
		if counters, err := disk.IOCounters(); err == nil {
			for name, counter := range counters {
				c.prevDisk[name] = counter
			}
		}
		c.prevDiskTime = time.Now()
		c.mu.Unlock()
	}()

	// Network
	wg.Add(1)
	go func() {
		defer wg.Done()
		if netIO, err := net.IOCountersWithContext(ctx, true); err == nil {
			var networks []NetworkInfo
			for _, io := range netIO {
				if io.Name == "Loopback Pseudo-Interface 1" || (io.BytesSent == 0 && io.BytesRecv == 0) {
					continue
				}
				networks = append(networks, NetworkInfo{
					Name:        io.Name,
					BytesSent:   io.BytesSent,
					BytesRecv:   io.BytesRecv,
					PacketsSent: io.PacketsSent,
					PacketsRecv: io.PacketsRecv,
				})
			}
			mu.Lock()
			snapshot.Networks = networks
			mu.Unlock()
		}

		c.mu.Lock()
		c.prevNet = make(map[string]net.IOCountersStat)
		if counters, err := net.IOCountersWithContext(ctx, true); err == nil {
			for _, ctr := range counters {
				c.prevNet[ctr.Name] = ctr
			}
		}
		c.prevNetTime = time.Now()
		c.mu.Unlock()
	}()

	// Top Processes
	wg.Add(1)
	go func() {
		defer wg.Done()
		procs, err := process.ProcessesWithContext(ctx)
		if err != nil {
			return
		}

		var procInfos []ProcessInfo
		for _, p := range procs {
			name, err := p.NameWithContext(ctx)
			if err != nil {
				continue
			}
			cmdline, _ := p.CmdlineWithContext(ctx)
			var memBytes uint64
			if memInfo, err := p.MemoryInfoWithContext(ctx); err == nil && memInfo != nil {
				memBytes = memInfo.RSS
			}
			cpuPercent, _ := p.CPUPercentWithContext(ctx)
			memPercent, _ := p.MemoryPercentWithContext(ctx)

			if cpuPercent > 0.1 || memPercent > 0.1 {
				procInfos = append(procInfos, ProcessInfo{
					PID:         p.Pid,
					Name:        name,
					CPU:         cpuPercent,
					Memory:      memPercent,
					Command:     cmdline,
					MemoryBytes: memBytes,
				})
			}
		}

		for i := 0; i < len(procInfos)-1; i++ {
			for j := i + 1; j < len(procInfos); j++ {
				if procInfos[j].CPU > procInfos[i].CPU {
					procInfos[i], procInfos[j] = procInfos[j], procInfos[i]
				}
			}
		}

		if len(procInfos) > 5 {
			procInfos = procInfos[:5]
		}

		mu.Lock()
		snapshot.TopProcesses = procInfos
		mu.Unlock()
	}()

	wg.Wait()

	snapshot.HealthScore, snapshot.HealthMessage = calculateHealthScore(snapshot)

	return snapshot
}

func (c *Collector) getNetworkRates(networks []NetworkInfo) map[string][2]float64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	rates := make(map[string][2]float64)
	if len(c.prevNet) == 0 || c.prevNetTime.IsZero() {
		return rates
	}

	elapsed := time.Since(c.prevNetTime).Seconds()
	if elapsed <= 0 {
		return rates
	}

	for _, n := range networks {
		if prev, ok := c.prevNet[n.Name]; ok {
			rxRate := float64(n.BytesRecv-prev.BytesRecv) / elapsed / 1048576.0
			txRate := float64(n.BytesSent-prev.BytesSent) / elapsed / 1048576.0
			if rxRate < 0 {
				rxRate = 0
			}
			if txRate < 0 {
				txRate = 0
			}
			rates[n.Name] = [2]float64{rxRate, txRate}
		}
	}
	return rates
}

func (c *Collector) getDiskIORates() (readMBs, writeMBs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.prevDisk) == 0 || c.prevDiskTime.IsZero() {
		return 0, 0
	}

	elapsed := time.Since(c.prevDiskTime).Seconds()
	if elapsed <= 0 {
		return 0, 0
	}

	current, err := disk.IOCounters()
	if err != nil {
		return 0, 0
	}

	var totalReadBytes, totalWriteBytes uint64
	var prevReadBytes, prevWriteBytes uint64
	for name, cur := range current {
		totalReadBytes += cur.ReadBytes
		totalWriteBytes += cur.WriteBytes
		if prev, ok := c.prevDisk[name]; ok {
			prevReadBytes += prev.ReadBytes
			prevWriteBytes += prev.WriteBytes
		}
	}

	readRate := float64(totalReadBytes-prevReadBytes) / elapsed / 1048576.0
	writeRate := float64(totalWriteBytes-prevWriteBytes) / elapsed / 1048576.0
	if readRate < 0 {
		readRate = 0
	}
	if writeRate < 0 {
		writeRate = 0
	}
	return readRate, writeRate
}

func calculateHealthScore(s MetricsSnapshot) (int, string) {
	score := 100
	var issues []string

	if s.CPUPercent > 90 {
		score -= 30
		issues = append(issues, "High CPU")
	} else if s.CPUPercent > 70 {
		score -= 15
		issues = append(issues, "Elevated CPU")
	}

	if s.MemPercent > 90 {
		score -= 25
		issues = append(issues, "High Memory")
	} else if s.MemPercent > 80 {
		score -= 12
		issues = append(issues, "Elevated Memory")
	}

	for _, d := range s.Disks {
		if d.UsedPercent > 95 {
			score -= 20
			issues = append(issues, fmt.Sprintf("Disk %s Critical", d.Device))
			break
		} else if d.UsedPercent > 85 {
			score -= 10
			issues = append(issues, fmt.Sprintf("Disk %s Low", d.Device))
			break
		}
	}

	if s.SwapPercent > 80 {
		score -= 10
		issues = append(issues, "High Swap")
	}

	if score < 0 {
		score = 0
	}

	msg := "Excellent"
	if len(issues) > 0 {
		msg = strings.Join(issues, ", ")
	} else if score >= 90 {
		msg = "Excellent"
	} else if score >= 70 {
		msg = "Good"
	} else if score >= 50 {
		msg = "Fair"
	} else {
		msg = "Poor"
	}

	return score, msg
}

// JSON output types

type jsonOutput struct {
	CollectedAt    string        `json:"collected_at"`
	Host           string        `json:"host"`
	Platform       string        `json:"platform"`
	UptimeSeconds  uint64        `json:"uptime_seconds"`
	Procs          int           `json:"procs"`
	HealthScore    int           `json:"health_score"`
	HealthScoreMsg string        `json:"health_score_msg"`
	Hardware       jsonHardware  `json:"hardware"`
	CPU            jsonCPU       `json:"cpu"`
	Memory         jsonMemory    `json:"memory"`
	DiskIO         jsonDiskIO    `json:"disk_io"`
	Disks          []jsonDisk    `json:"disks"`
	Network        []jsonNetwork `json:"network"`
	TopProcesses   []jsonProcess `json:"top_processes"`
	Batteries      *jsonBattery  `json:"batteries"`
	Thermal        jsonThermal   `json:"thermal"`
	GPU            []jsonGPU     `json:"gpu"`
	Proxy          interface{}   `json:"proxy"`
	Bluetooth      interface{}   `json:"bluetooth"`
}

type jsonHardware struct {
	Model     string `json:"model"`
	CPUModel  string `json:"cpu_model"`
	TotalRAM  string `json:"total_ram"`
	DiskSize  string `json:"disk_size"`
	OSVersion string `json:"os_version"`
}

type jsonCPU struct {
	Usage      float64   `json:"usage"`
	PerCore    []float64 `json:"per_core"`
	Load1      float64   `json:"load1"`
	Load5      float64   `json:"load5"`
	Load15     float64   `json:"load15"`
	CoreCount  int       `json:"core_count"`
	LogicalCPU int       `json:"logical_cpu"`
}

type jsonMemory struct {
	Used        uint64  `json:"used"`
	Total       uint64  `json:"total"`
	Available   uint64  `json:"available"`
	UsedPercent float64 `json:"used_percent"`
	SwapUsed    uint64  `json:"swap_used"`
	SwapTotal   uint64  `json:"swap_total"`
	Pressure    string  `json:"pressure"`
}

type jsonDiskIO struct {
	ReadRate  float64 `json:"read_rate"`
	WriteRate float64 `json:"write_rate"`
}

type jsonDisk struct {
	Mount       string  `json:"mount"`
	Used        uint64  `json:"used"`
	Total       uint64  `json:"total"`
	UsedPercent float64 `json:"used_percent"`
	External    bool    `json:"external"`
}

type jsonNetwork struct {
	Name      string  `json:"name"`
	RxRateMBs float64 `json:"rx_rate_mbs"`
	TxRateMBs float64 `json:"tx_rate_mbs"`
	IP        string  `json:"ip"`
}

type jsonProcess struct {
	PID         int32   `json:"pid"`
	Name        string  `json:"name"`
	Command     string  `json:"command"`
	CPU         float64 `json:"cpu"`
	Memory      float32 `json:"memory"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

type jsonBattery struct {
	Percent    int  `json:"percent"`
	IsCharging bool `json:"is_charging"`
}

type jsonThermal struct {
	CPUTemp     float64 `json:"cpu_temp"`
	GPUTemp     float64 `json:"gpu_temp"`
	FanSpeed    int     `json:"fan_speed"`
	FanCount    int     `json:"fan_count"`
	SystemPower int     `json:"system_power"`
}

type jsonGPU struct {
	Name        string `json:"name"`
	Usage       int    `json:"usage"`
	MemoryUsed  uint64 `json:"memory_used"`
	MemoryTotal uint64 `json:"memory_total"`
	CoreCount   int    `json:"core_count"`
}

func getInterfaceIP(name string) string {
	ifaces, err := gonet.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Name == name {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipNet, ok := addr.(*gonet.IPNet); ok && ipNet.IP.To4() != nil {
					return ipNet.IP.String()
				}
			}
		}
	}
	return ""
}

func getGPUNames() []jsonGPU {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_VideoController).Name")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}

	var gpus []jsonGPU
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		name := strings.TrimSpace(line)
		if name != "" {
			gpus = append(gpus, jsonGPU{
				Name:        name,
				Usage:       -1,
				MemoryUsed:  0,
				MemoryTotal: 0,
				CoreCount:   0,
			})
		}
	}
	return gpus
}

func getPhysicalCoreCount() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_Processor | Measure-Object -Property NumberOfCores -Sum).Sum")
	output, err := cmd.Output()
	if err != nil {
		return runtime.NumCPU()
	}
	cores, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil || cores <= 0 {
		return runtime.NumCPU()
	}
	return cores
}

func getProcessCount() int {
	procs, err := process.Processes()
	if err != nil {
		return 0
	}
	return len(procs)
}

func formatRAMSize(bytes uint64) string {
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb >= 1 {
		return fmt.Sprintf("%d GB", int(gb+0.5))
	}
	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%d MB", int(mb+0.5))
}

func formatDiskSize(bytes uint64) string {
	tb := float64(bytes) / (1024 * 1024 * 1024 * 1024)
	if tb >= 1 {
		return fmt.Sprintf("%d TB", int(tb+0.5))
	}
	gb := float64(bytes) / (1024 * 1024 * 1024)
	if gb >= 1 {
		return fmt.Sprintf("%d GB", int(gb+0.5))
	}
	mb := float64(bytes) / (1024 * 1024)
	return fmt.Sprintf("%d MB", int(mb+0.5))
}

func memoryPressure(percent float64) string {
	if percent > 85 {
		return "high"
	} else if percent > 70 {
		return "medium"
	}
	return "normal"
}

func isExternalDisk(d DiskInfo) bool {
	device := strings.TrimSuffix(d.Device, ":")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_LogicalDisk -Filter \"DeviceID='"+device+":'\").DriveType")
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	driveType, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return false
	}
	return driveType == 2 // 2 = Removable
}

func (c *Collector) cachedProcessCount() int {
	c.procCountOnce.Do(func() {
		procs, err := process.Processes()
		if err == nil {
			c.procCount = len(procs)
		}
	})
	return c.procCount
}

func (c *Collector) cachedCoreCount() int {
	c.coreCountOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "powershell", "-Command",
			"(Get-CimInstance Win32_Processor | Measure-Object -Property NumberOfCores -Sum).Sum")
		output, err := cmd.Output()
		if err == nil {
			cores, convErr := strconv.Atoi(strings.TrimSpace(string(output)))
			if convErr == nil && cores > 0 {
				c.physicalCores = cores
				return
			}
		}
		c.physicalCores = runtime.NumCPU()
	})
	return c.physicalCores
}

func buildJSONOutput(s MetricsSnapshot, c *Collector) jsonOutput {
	procs := c.cachedProcessCount()
	physicalCores := c.cachedCoreCount()
	gpus := getGPUNames()

	perCore := s.CPUPerCore
	if perCore == nil {
		perCore = []float64{}
	}

	var avail uint64
	if s.MemTotal > s.MemUsed {
		avail = s.MemTotal - s.MemUsed
	}

	diskReadRate, diskWriteRate := c.getDiskIORates()
	netRates := c.getNetworkRates(s.Networks)

	hwDiskSize := ""
	if len(s.Disks) > 0 {
		hwDiskSize = formatDiskSize(s.Disks[0].Total)
	}

	out := jsonOutput{
		CollectedAt:    s.CollectedAt.Format(time.RFC3339Nano),
		Host:           s.Hostname,
		Platform:       s.Platform,
		UptimeSeconds:  uint64(s.Uptime.Seconds()),
		Procs:          procs,
		HealthScore:    s.HealthScore,
		HealthScoreMsg: s.HealthMessage,
		Hardware: jsonHardware{
			Model:     "Desktop PC",
			CPUModel:  s.CPUModel,
			TotalRAM:  formatRAMSize(s.MemTotal),
			DiskSize:  hwDiskSize,
			OSVersion: s.Platform,
		},
		CPU: jsonCPU{
			Usage:      s.CPUPercent,
			PerCore:    perCore,
			Load1:      0,
			Load5:      0,
			Load15:     0,
			CoreCount:  physicalCores,
			LogicalCPU: runtime.NumCPU(),
		},
		Memory: jsonMemory{
			Used:        s.MemUsed,
			Total:       s.MemTotal,
			Available:   avail,
			UsedPercent: s.MemPercent,
			SwapUsed:    s.SwapUsed,
			SwapTotal:   s.SwapTotal,
			Pressure:    memoryPressure(s.MemPercent),
		},
		DiskIO: jsonDiskIO{
			ReadRate:  diskReadRate,
			WriteRate: diskWriteRate,
		},
		Thermal: jsonThermal{
			CPUTemp:     0,
			GPUTemp:     0,
			FanSpeed:    0,
			FanCount:    0,
			SystemPower: 0,
		},
		Proxy:     nil,
		Bluetooth: nil,
	}

	for _, d := range s.Disks {
		out.Disks = append(out.Disks, jsonDisk{
			Mount:       d.Device,
			Used:        d.Used,
			Total:       d.Total,
			UsedPercent: d.UsedPercent,
			External:    isExternalDisk(d),
		})
	}
	if out.Disks == nil {
		out.Disks = []jsonDisk{}
	}

	for _, n := range s.Networks {
		rate := netRates[n.Name]
		out.Network = append(out.Network, jsonNetwork{
			Name:      n.Name,
			RxRateMBs: rate[0],
			TxRateMBs: rate[1],
			IP:        getInterfaceIP(n.Name),
		})
	}
	if out.Network == nil {
		out.Network = []jsonNetwork{}
	}

	for _, p := range s.TopProcesses {
		out.TopProcesses = append(out.TopProcesses, jsonProcess{
			PID:         p.PID,
			Name:        p.Name,
			Command:     p.Command,
			CPU:         p.CPU,
			Memory:      p.Memory,
			MemoryBytes: p.MemoryBytes,
		})
	}
	if out.TopProcesses == nil {
		out.TopProcesses = []jsonProcess{}
	}

	batPercent, batCharging, batPresent := getBatteryInfo()
	if batPresent {
		out.Batteries = &jsonBattery{
			Percent:    batPercent,
			IsCharging: batCharging,
		}
	}

	if len(gpus) > 0 {
		out.GPU = gpus
	} else {
		out.GPU = []jsonGPU{}
	}

	return out
}

func collectJSON(c *Collector) {
	snapshot := c.Collect()
	out := buildJSONOutput(snapshot, c)

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
}

// Model for Bubble Tea
type model struct {
	collector  *Collector
	metrics    MetricsSnapshot
	animFrame  int
	catHidden  bool
	ready      bool
	collecting bool
	width      int
	height     int
}

// Messages
type tickMsg time.Time
type metricsMsg MetricsSnapshot

func newModel() model {
	return model{
		collector: NewCollector(),
		animFrame: 0,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		m.collectMetrics(),
		tickCmd(),
	)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) collectMetrics() tea.Cmd {
	return func() tea.Msg {
		return metricsMsg(m.collector.Collect())
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "c":
			m.catHidden = !m.catHidden
		case "r":
			m.collecting = true
			return m, m.collectMetrics()
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
	case tickMsg:
		m.animFrame++
		if m.animFrame%2 == 0 && !m.collecting {
			return m, tea.Batch(
				m.collectMetrics(),
				tickCmd(),
			)
		}
		return m, tickCmd()
	case metricsMsg:
		m.metrics = MetricsSnapshot(msg)
		m.ready = true
		m.collecting = false
	}
	return m, nil
}

func (m model) View() string {
	if !m.ready {
		return "\n  Loading system metrics..."
	}

	var b strings.Builder

	moleFrame := getMoleFrame(m.animFrame, m.catHidden)

	b.WriteString("\n")
	b.WriteString(titleStyle.Render("  🐹 Mole System Status"))
	b.WriteString("  ")
	b.WriteString(moleFrame)
	b.WriteString("\n\n")

	healthColor := okStyle
	if m.metrics.HealthScore < 50 {
		healthColor = dangerStyle
	} else if m.metrics.HealthScore < 70 {
		healthColor = warnStyle
	}
	b.WriteString(fmt.Sprintf("  Health: %s  %s\n\n",
		healthColor.Render(fmt.Sprintf("%d%%", m.metrics.HealthScore)),
		dimStyle.Render(m.metrics.HealthMessage),
	))

	b.WriteString(headerStyle.Render("  📍 System"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Host:"), valueStyle.Render(m.metrics.Hostname)))
	b.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("OS:"), valueStyle.Render(m.metrics.Platform)))
	b.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Uptime:"), valueStyle.Render(formatDuration(m.metrics.Uptime))))
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("  ⚡ CPU"))
	b.WriteString("\n")
	cpuColor := getPercentColor(m.metrics.CPUPercent)
	b.WriteString(fmt.Sprintf("  %s %s\n", labelStyle.Render("Model:"), valueStyle.Render(truncateString(m.metrics.CPUModel, 50))))
	b.WriteString(fmt.Sprintf("  %s %s (%d cores)\n",
		labelStyle.Render("Usage:"),
		cpuColor.Render(fmt.Sprintf("%.1f%%", m.metrics.CPUPercent)),
		m.metrics.CPUCores,
	))
	b.WriteString(fmt.Sprintf("  %s\n", renderProgressBar(m.metrics.CPUPercent, 30)))
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("  🧠 Memory"))
	b.WriteString("\n")
	memColor := getPercentColor(m.metrics.MemPercent)
	b.WriteString(fmt.Sprintf("  %s %s / %s %s\n",
		labelStyle.Render("RAM:"),
		memColor.Render(formatBytes(m.metrics.MemUsed)),
		valueStyle.Render(formatBytes(m.metrics.MemTotal)),
		memColor.Render(fmt.Sprintf("(%.1f%%)", m.metrics.MemPercent)),
	))
	b.WriteString(fmt.Sprintf("  %s\n", renderProgressBar(m.metrics.MemPercent, 30)))
	if m.metrics.SwapTotal > 0 {
		b.WriteString(fmt.Sprintf("  %s %s / %s\n",
			labelStyle.Render("Swap:"),
			valueStyle.Render(formatBytes(m.metrics.SwapUsed)),
			valueStyle.Render(formatBytes(m.metrics.SwapTotal)),
		))
	}
	b.WriteString("\n")

	b.WriteString(headerStyle.Render("  💾 Disks"))
	b.WriteString("\n")
	for _, d := range m.metrics.Disks {
		diskColor := getPercentColor(d.UsedPercent)
		b.WriteString(fmt.Sprintf("  %s %s / %s %s\n",
			labelStyle.Render(d.Device),
			diskColor.Render(formatBytes(d.Used)),
			valueStyle.Render(formatBytes(d.Total)),
			diskColor.Render(fmt.Sprintf("(%.1f%%)", d.UsedPercent)),
		))
		b.WriteString(fmt.Sprintf("  %s\n", renderProgressBar(d.UsedPercent, 30)))
	}
	b.WriteString("\n")

	if len(m.metrics.TopProcesses) > 0 {
		b.WriteString(headerStyle.Render("  📊 Top Processes"))
		b.WriteString("\n")
		for _, p := range m.metrics.TopProcesses {
			b.WriteString(fmt.Sprintf("  %s %s (CPU: %.1f%%, Mem: %.1f%%)\n",
				dimStyle.Render(fmt.Sprintf("[%d]", p.PID)),
				valueStyle.Render(truncateString(p.Name, 20)),
				p.CPU,
				p.Memory,
			))
		}
		b.WriteString("\n")
	}

	if len(m.metrics.Networks) > 0 {
		b.WriteString(headerStyle.Render("  🌐 Network"))
		b.WriteString("\n")
		for i, n := range m.metrics.Networks {
			if i >= 3 {
				break
			}
			b.WriteString(fmt.Sprintf("  %s ↑%s ↓%s\n",
				labelStyle.Render(truncateString(n.Name, 20)+":"),
				valueStyle.Render(formatBytes(n.BytesSent)),
				valueStyle.Render(formatBytes(n.BytesRecv)),
			))
		}
		b.WriteString("\n")
	}

	b.WriteString(dimStyle.Render("  [q] quit  [r] refresh  [c] toggle mole"))
	b.WriteString("\n")

	return b.String()
}

func getMoleFrame(frame int, hidden bool) string {
	if hidden {
		return ""
	}
	frames := []string{
		"🐹",
		"🐹.",
		"🐹..",
		"🐹...",
	}
	return frames[frame%len(frames)]
}

func renderProgressBar(percent float64, width int) string {
	filled := int(percent / 100 * float64(width))
	if filled > width {
		filled = width
	}
	if filled < 0 {
		filled = 0
	}

	color := okStyle
	if percent > 85 {
		color = dangerStyle
	} else if percent > 70 {
		color = warnStyle
	}

	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	return color.Render(bar)
}

func getPercentColor(percent float64) lipgloss.Style {
	if percent > 85 {
		return dangerStyle
	} else if percent > 70 {
		return warnStyle
	}
	return okStyle
}

func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := uint64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func getWindowsVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_OperatingSystem).Caption")
	output, err := cmd.Output()
	if err != nil {
		return "Windows"
	}
	return strings.TrimSpace(string(output))
}

func getBatteryInfo() (int, bool, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_Battery).EstimatedChargeRemaining")
	output, err := cmd.Output()
	if err != nil {
		return 0, false, false
	}

	percent, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return 0, false, false
	}

	cmdStatus := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-CimInstance Win32_Battery).BatteryStatus")
	statusOutput, _ := cmdStatus.Output()
	status, _ := strconv.Atoi(strings.TrimSpace(string(statusOutput)))
	isCharging := status == 2

	return percent, isCharging, true
}

func main() {
	jsonFlag := flag.Bool("json", false, "Output metrics as JSON and exit")
	flag.Parse()

	if *jsonFlag {
		c := NewCollector()
		collectJSON(c)
		os.Exit(0)
	}

	p := tea.NewProgram(newModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
