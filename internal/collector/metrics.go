package collector

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
)

// SystemMetrics is a point-in-time snapshot of the entire machine's health.
type SystemMetrics struct {
	Timestamp time.Time

	// CPU
	CPUTotalPercent float64
	CPUPerCore      []float64

	// Memory
	MemTotal     uint64
	MemUsed      uint64
	MemAvailable uint64
	MemPercent   float64

	// Swap
	SwapTotal   uint64
	SwapUsed    uint64
	SwapPercent float64

	// Network (aggregate delta since last poll)
	NetBytesSent   uint64
	NetBytesRecv   uint64
	NetPacketsSent uint64
	NetPacketsRecv uint64

	// Disk (root partition)
	DiskTotal   uint64
	DiskUsed    uint64
	DiskPercent float64
	DiskReadMB  float64
	DiskWriteMB float64

	// OS info
	OS   string
	Arch string
	CPUs int
}

// MetricsCollector gathers system telemetry on a fixed interval and
// makes the latest snapshot available without blocking callers.
type MetricsCollector struct {
	mu      sync.RWMutex
	latest  SystemMetrics
	history []SystemMetrics // rolling window of last N snapshots

	prevNet  []net.IOCountersStat
	prevDisk map[string]disk.IOCountersStat

	historySize int
}

// NewMetricsCollector creates a ready-to-use MetricsCollector.
func NewMetricsCollector(historySize int) *MetricsCollector {
	if historySize <= 0 {
		historySize = 60
	}
	return &MetricsCollector{
		historySize: historySize,
		history:     make([]SystemMetrics, 0, historySize),
		prevDisk:    make(map[string]disk.IOCountersStat),
	}
}

// Collect performs a single metrics poll and updates the internal snapshot.
// It is safe to call from any goroutine.
func (c *MetricsCollector) Collect(ctx context.Context) error {
	snap := SystemMetrics{
		Timestamp: time.Now(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		CPUs:      runtime.NumCPU(),
	}

	var wg sync.WaitGroup
	errs := make(chan error, 5)

	// --- CPU ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		total, err := cpu.PercentWithContext(ctx, 500*time.Millisecond, false)
		if err != nil {
			errs <- fmt.Errorf("cpu total: %w", err)
			return
		}
		if len(total) > 0 {
			snap.CPUTotalPercent = total[0]
		}
		perCore, err := cpu.PercentWithContext(ctx, 0, true)
		if err != nil {
			errs <- fmt.Errorf("cpu per-core: %w", err)
			return
		}
		snap.CPUPerCore = perCore
	}()

	// --- Memory ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		vm, err := mem.VirtualMemoryWithContext(ctx)
		if err != nil {
			errs <- fmt.Errorf("vmem: %w", err)
			return
		}
		snap.MemTotal = vm.Total
		snap.MemUsed = vm.Used
		snap.MemAvailable = vm.Available
		snap.MemPercent = vm.UsedPercent

		sw, err := mem.SwapMemoryWithContext(ctx)
		if err == nil {
			snap.SwapTotal = sw.Total
			snap.SwapUsed = sw.Used
			snap.SwapPercent = sw.UsedPercent
		}
	}()

	// --- Network ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		counters, err := net.IOCountersWithContext(ctx, false) // false = aggregate
		if err != nil {
			errs <- fmt.Errorf("net io: %w", err)
			return
		}
		if len(counters) > 0 {
			cur := counters[0]
			c.mu.RLock()
			prev := c.prevNet
			c.mu.RUnlock()

			if len(prev) > 0 {
				snap.NetBytesSent = cur.BytesSent - prev[0].BytesSent
				snap.NetBytesRecv = cur.BytesRecv - prev[0].BytesRecv
				snap.NetPacketsSent = cur.PacketsSent - prev[0].PacketsSent
				snap.NetPacketsRecv = cur.PacketsRecv - prev[0].PacketsRecv
			}

			c.mu.Lock()
			c.prevNet = counters
			c.mu.Unlock()
		}
	}()

	// --- Disk ---
	wg.Add(1)
	go func() {
		defer wg.Done()
		usage, err := disk.UsageWithContext(ctx, "/")
		if err != nil {
			errs <- fmt.Errorf("disk usage: %w", err)
			return
		}
		snap.DiskTotal = usage.Total
		snap.DiskUsed = usage.Used
		snap.DiskPercent = usage.UsedPercent

		ioMap, err := disk.IOCountersWithContext(ctx)
		if err == nil {
			c.mu.Lock()
			var readBytes, writeBytes uint64
			for dev, cur := range ioMap {
				if prev, ok := c.prevDisk[dev]; ok {
					readBytes += cur.ReadBytes - prev.ReadBytes
					writeBytes += cur.WriteBytes - prev.WriteBytes
				}
				c.prevDisk[dev] = cur
			}
			c.mu.Unlock()
			snap.DiskReadMB = float64(readBytes) / 1024 / 1024
			snap.DiskWriteMB = float64(writeBytes) / 1024 / 1024
		}
	}()

	wg.Wait()
	close(errs)

	// Collect non-fatal errors (partial data still useful)
	var firstErr error
	for e := range errs {
		if firstErr == nil {
			firstErr = e
		}
	}

	c.mu.Lock()
	c.latest = snap
	if len(c.history) >= c.historySize {
		c.history = c.history[1:]
	}
	c.history = append(c.history, snap)
	c.mu.Unlock()

	return firstErr
}

// Latest returns the most recent snapshot (zero-value if none collected yet).
func (c *MetricsCollector) Latest() SystemMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.latest
}

// History returns a defensive copy of the rolling snapshot window.
func (c *MetricsCollector) History() []SystemMetrics {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]SystemMetrics, len(c.history))
	copy(out, c.history)
	return out
}

// CPUSparkline returns the last n CPU% values suitable for a sparkline widget.
func (c *MetricsCollector) CPUSparkline(n int) []float64 {
	h := c.History()
	if n > len(h) {
		n = len(h)
	}
	out := make([]float64, n)
	for i, snap := range h[len(h)-n:] {
		out[i] = snap.CPUTotalPercent
	}
	return out
}
