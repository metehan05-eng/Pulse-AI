package collector

import (
	"context"
	"fmt"

	"github.com/shirou/gopsutil/v3/process"
)

// GetProcessByPID returns a ProcessInfo for a specific PID.
// Used by the CLI analyze command when a PID is not in the top-N list.
func GetProcessByPID(pid int32) (ProcessInfo, error) {
	p, err := process.NewProcess(pid)
	if err != nil {
		return ProcessInfo{}, fmt.Errorf("process %d: %w", pid, err)
	}
	conns, _ := netConn(context.Background())
	info, err := inspectProcess(context.Background(), p, conns)
	if err != nil {
		return ProcessInfo{}, err
	}
	return info, nil
}
