package collector_test

import (
	"context"
	"testing"
	"time"

	"github.com/spectre05/pulseai/internal/collector"
)

// ─── MetricsCollector tests ───────────────────────────────────────────────────

func TestMetricsCollector_Collect(t *testing.T) {
	mc := collector.NewMetricsCollector(10)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := mc.Collect(ctx); err != nil {
		// Non-fatal: some CI environments restrict /proc access
		t.Logf("collect returned non-nil error (may be CI): %v", err)
	}

	snap := mc.Latest()
	if snap.Timestamp.IsZero() {
		t.Fatal("expected non-zero timestamp after Collect")
	}
	if snap.MemTotal == 0 {
		t.Error("expected non-zero MemTotal")
	}
	if snap.CPUs <= 0 {
		t.Errorf("expected positive CPU count, got %d", snap.CPUs)
	}
}

func TestMetricsCollector_History(t *testing.T) {
	const histSize = 5
	mc := collector.NewMetricsCollector(histSize)
	ctx := context.Background()

	// Collect more than histSize times to verify rolling window
	for i := 0; i < histSize+3; i++ {
		_ = mc.Collect(ctx)
	}

	h := mc.History()
	if len(h) > histSize {
		t.Errorf("history length %d exceeds window %d", len(h), histSize)
	}
}

func TestMetricsCollector_CPUSparkline(t *testing.T) {
	mc := collector.NewMetricsCollector(30)
	ctx := context.Background()
	for i := 0; i < 10; i++ {
		_ = mc.Collect(ctx)
	}

	spark := mc.CPUSparkline(5)
	if len(spark) > 5 {
		t.Errorf("sparkline returned %d values, want ≤5", len(spark))
	}
}

func TestMetricsCollector_ConcurrentAccess(t *testing.T) {
	mc := collector.NewMetricsCollector(20)
	ctx := context.Background()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			_ = mc.Collect(ctx)
		}
		close(done)
	}()
	for i := 0; i < 50; i++ {
		_ = mc.Latest()
		_ = mc.History()
	}
	<-done
}

// ─── ProcessCollector tests ───────────────────────────────────────────────────

func TestProcessCollector_Collect(t *testing.T) {
	pc := collector.NewProcessCollector(5, 80, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pc.Collect(ctx); err != nil {
		t.Logf("collect error (may be CI): %v", err)
	}

	procs := pc.Latest()
	if len(procs) == 0 {
		t.Skip("no processes collected — likely restricted CI environment")
	}
	if len(procs) > 5 {
		t.Errorf("expected ≤5 processes, got %d", len(procs))
	}
}

func TestProcessCollector_GetByPID(t *testing.T) {
	pc := collector.NewProcessCollector(20, 0, 0)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := pc.Collect(ctx); err != nil {
		t.Skip("collect failed:", err)
	}

	procs := pc.Latest()
	if len(procs) == 0 {
		t.Skip("no processes collected")
	}

	target := procs[0]
	got, ok := pc.GetByPID(target.PID)
	if !ok {
		t.Fatalf("GetByPID(%d) returned false", target.PID)
	}
	if got.PID != target.PID {
		t.Errorf("got PID %d, want %d", got.PID, target.PID)
	}
}

// ─── Anomaly detection unit tests ─────────────────────────────────────────────

func TestFormatBytes(t *testing.T) {
	cases := []struct {
		input    uint64
		expected string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KiB"},
		{1024 * 1024, "1.0 MiB"},
		{1024 * 1024 * 1024, "1.0 GiB"},
	}
	for _, tc := range cases {
		got := collector.FormatBytes(tc.input)
		if got != tc.expected {
			t.Errorf("FormatBytes(%d) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}
