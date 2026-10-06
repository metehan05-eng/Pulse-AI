package collector

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	psnet "github.com/shirou/gopsutil/v3/net"
	"github.com/shirou/gopsutil/v3/process"
)

// AnomalyFlag represents a single suspicious indicator on a process.
type AnomalyFlag struct {
	Severity string // "low", "medium", "high"
	Reason   string
}

// ProcessInfo holds telemetry and anomaly data for a single process.
type ProcessInfo struct {
	PID        int32
	Name       string
	Username   string
	Exe        string
	CmdLine    string
	CPUPercent float64
	MemPercent float32
	MemRSS     uint64 // bytes
	NumThreads int32
	Status     string
	OpenFiles  int
	Anomalies  []AnomalyFlag
}

// HasAnomalies returns true if the process has been flagged.
func (p ProcessInfo) HasAnomalies() bool { return len(p.Anomalies) > 0 }

// MaxSeverity returns the highest severity across all flags ("" if none).
func (p ProcessInfo) MaxSeverity() string {
	order := map[string]int{"low": 1, "medium": 2, "high": 3}
	max := 0
	sev := ""
	for _, a := range p.Anomalies {
		if v := order[a.Severity]; v > max {
			max = v
			sev = a.Severity
		}
	}
	return sev
}

// ProcessCollector inspects the running process list.
type ProcessCollector struct {
	mu      sync.RWMutex
	latest  []ProcessInfo
	topN    int
	cpuThresh float64
	memThresh float64
}

// NewProcessCollector creates a ProcessCollector.
func NewProcessCollector(topN int, cpuThresh, memThresh float64) *ProcessCollector {
	if topN <= 0 {
		topN = 10
	}
	return &ProcessCollector{
		topN:      topN,
		cpuThresh: cpuThresh,
		memThresh: memThresh,
	}
}

// Collect samples the process list, ranks by CPU+memory, and runs anomaly checks.
func (c *ProcessCollector) Collect(ctx context.Context) error {
	procs, err := process.ProcessesWithContext(ctx)
	if err != nil {
		return fmt.Errorf("list processes: %w", err)
	}

	// Gather connections once for the anomaly detector
	conns, _ := netConn(ctx)

	results := make([]ProcessInfo, 0, len(procs))
	sem := make(chan struct{}, 64) // throttle goroutines
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range procs {
		p := p
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			info, err := inspectProcess(ctx, p, conns)
			if err != nil {
				return
			}
			mu.Lock()
			results = append(results, info)
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Sort: anomalous processes first, then by CPU desc
	sort.Slice(results, func(i, j int) bool {
		ai, aj := len(results[i].Anomalies), len(results[j].Anomalies)
		if ai != aj {
			return ai > aj
		}
		return results[i].CPUPercent > results[j].CPUPercent
	})

	top := results
	if len(top) > c.topN {
		top = top[:c.topN]
	}

	c.mu.Lock()
	c.latest = top
	c.mu.Unlock()
	return nil
}

// Latest returns the most-recently collected process list.
func (c *ProcessCollector) Latest() []ProcessInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]ProcessInfo, len(c.latest))
	copy(out, c.latest)
	return out
}

// GetByPID returns process info for the given PID, or false if not found.
func (c *ProcessCollector) GetByPID(pid int32) (ProcessInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.latest {
		if p.PID == pid {
			return p, true
		}
	}
	return ProcessInfo{}, false
}

// ─── helpers ────────────────────────────────────────────────────────────────

func inspectProcess(ctx context.Context, p *process.Process, conns []psnet.ConnectionStat) (ProcessInfo, error) {
	info := ProcessInfo{PID: p.Pid}

	// Best-effort — ignore individual field errors
	info.Name, _ = p.NameWithContext(ctx)
	info.Username, _ = p.UsernameWithContext(ctx)
	info.Exe, _ = p.ExeWithContext(ctx)

	if cmd, err := p.CmdlineWithContext(ctx); err == nil {
		info.CmdLine = cmd
	}

	if cpu, err := p.CPUPercentWithContext(ctx); err == nil {
		info.CPUPercent = cpu
	}

	if mem, err := p.MemoryPercentWithContext(ctx); err == nil {
		info.MemPercent = mem
	}

	if mi, err := p.MemoryInfoWithContext(ctx); err == nil && mi != nil {
		info.MemRSS = mi.RSS
	}

	if t, err := p.NumThreadsWithContext(ctx); err == nil {
		info.NumThreads = t
	}

	if s, err := p.StatusWithContext(ctx); err == nil && len(s) > 0 {
		info.Status = s[0]
	}

	if files, err := p.OpenFilesWithContext(ctx); err == nil {
		info.OpenFiles = len(files)
	}

	// Anomaly detection
	info.Anomalies = detectAnomalies(info, conns)

	return info, nil
}

// detectAnomalies applies heuristic rules to flag suspicious processes.
func detectAnomalies(info ProcessInfo, conns []psnet.ConnectionStat) []AnomalyFlag {
	var flags []AnomalyFlag

	exe := info.Exe

	// 1. Running from /tmp, /dev/shm, or hidden dot-dirs
	if strings.HasPrefix(exe, "/tmp/") ||
		strings.HasPrefix(exe, "/dev/shm") ||
		strings.Contains(exe, "/.") {
		flags = append(flags, AnomalyFlag{
			Severity: "high",
			Reason:   fmt.Sprintf("executable in suspicious path: %s", exe),
		})
	}

	// 2. Executable deleted on disk (common rootkit trick)
	if strings.HasSuffix(exe, "(deleted)") || strings.Contains(exe, " (deleted)") {
		flags = append(flags, AnomalyFlag{
			Severity: "high",
			Reason:   "executable deleted from disk while still running",
		})
	}

	// 3. High open-file count (potential descriptor exhaustion attack)
	if info.OpenFiles > 500 {
		flags = append(flags, AnomalyFlag{
			Severity: "medium",
			Reason:   fmt.Sprintf("unusually high open file count: %d", info.OpenFiles),
		})
	}

	// 4. High thread count
	if info.NumThreads > 200 {
		flags = append(flags, AnomalyFlag{
			Severity: "low",
			Reason:   fmt.Sprintf("high thread count: %d", info.NumThreads),
		})
	}

	// 5. Suspicious name patterns
	for _, pattern := range suspiciousNames {
		if strings.Contains(strings.ToLower(info.Name), pattern) {
			flags = append(flags, AnomalyFlag{
				Severity: "medium",
				Reason:   fmt.Sprintf("name matches suspicious pattern: %q", pattern),
			})
			break
		}
	}

	// 6. Listening on unexpected ports
	for _, conn := range conns {
		if conn.Pid != info.PID {
			continue
		}
		if conn.Status == "LISTEN" {
			port := conn.Laddr.Port
			if !isCommonPort(port) {
				flags = append(flags, AnomalyFlag{
					Severity: "medium",
					Reason:   fmt.Sprintf("listening on non-standard port %d", port),
				})
			}
		}
	}

	// 7. Command line contains base64 blob (common obfuscation)
	if containsBase64Blob(info.CmdLine) {
		flags = append(flags, AnomalyFlag{
			Severity: "high",
			Reason:   "command line contains embedded base64 payload",
		})
	}

	// 8. Executable outside standard system paths
	if exe != "" && !isStandardPath(exe) {
		flags = append(flags, AnomalyFlag{
			Severity: "low",
			Reason:   fmt.Sprintf("executable outside standard system paths: %s", exe),
		})
	}

	return flags
}

var suspiciousNames = []string{
	"miner", "xmrig", "kinsing", "masscan", "zmap",
	"nmap", "nikto", "sqlmap", "netcat", "nc.traditional",
	"cryptojack", "botnet",
}

var standardPrefixes = []string{
	"/usr/", "/bin/", "/sbin/", "/lib/", "/opt/",
	"/snap/", "/home/", "/root/",
}

func isStandardPath(exe string) bool {
	// Ignore empty / kernel threads
	if exe == "" {
		return true
	}
	for _, pfx := range standardPrefixes {
		if strings.HasPrefix(exe, pfx) {
			return true
		}
	}
	return false
}

func isCommonPort(port uint32) bool {
	common := map[uint32]bool{
		22: true, 80: true, 443: true, 8080: true, 8443: true,
		3306: true, 5432: true, 6379: true, 27017: true,
		53: true, 25: true, 587: true, 993: true,
		3000: true, 4000: true, 5000: true, 8000: true, 9000: true,
	}
	return common[port]
}

// containsBase64Blob naively detects long base64-like strings in cmd args.
func containsBase64Blob(cmd string) bool {
	for _, token := range strings.Fields(cmd) {
		if len(token) >= 80 && isBase64(token) {
			return true
		}
	}
	return false
}

func isBase64(s string) bool {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	for _, c := range s {
		if !strings.ContainsRune(chars, c) {
			return false
		}
	}
	return true
}

func netConn(ctx context.Context) ([]psnet.ConnectionStat, error) {
	return psnet.ConnectionsWithContext(ctx, "all")
}

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
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// CleanExePath returns a shortened, display-friendly version of a path.
func CleanExePath(exe string) string {
	if exe == "" {
		return "[kernel]"
	}
	return filepath.Base(exe)
}

