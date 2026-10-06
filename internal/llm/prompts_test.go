package llm_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/spectre05/pulseai/internal/collector"
	"github.com/spectre05/pulseai/internal/llm"
)

// ─── Prompt serialization tests ───────────────────────────────────────────────

func TestBuildProcessAnalysisPrompt_Structure(t *testing.T) {
	proc := collector.ProcessInfo{
		PID:        1234,
		Name:       "test-proc",
		Exe:        "/tmp/test-proc",
		CmdLine:    "test-proc --flag",
		CPUPercent: 45.5,
		MemPercent: 2.1,
		NumThreads: 4,
		Anomalies: []collector.AnomalyFlag{
			{Severity: "high", Reason: "executable in suspicious path: /tmp/test-proc"},
		},
	}

	metrics := collector.SystemMetrics{
		CPUTotalPercent: 55.0,
		MemPercent:      70.0,
		MemUsed:         8 * 1024 * 1024 * 1024,
		MemTotal:        16 * 1024 * 1024 * 1024,
		DiskPercent:     40.0,
		NetBytesSent:    1024 * 1024,
		NetBytesRecv:    2 * 1024 * 1024,
	}

	req := llm.BuildProcessAnalysisPrompt(proc, metrics, nil, "qwen2.5-coder")

	if req.Model != "qwen2.5-coder" {
		t.Errorf("expected model %q, got %q", "qwen2.5-coder", req.Model)
	}
	if req.System == "" {
		t.Error("expected non-empty system prompt")
	}
	if req.Prompt == "" {
		t.Error("expected non-empty prompt")
	}
	if !req.Stream {
		t.Error("expected Stream=true")
	}

	// Verify JSON context is embedded in prompt
	if !strings.Contains(req.Prompt, `"pid": 1234`) {
		t.Error("prompt does not contain PID in JSON context")
	}
	if !strings.Contains(req.Prompt, "/tmp/test-proc") {
		t.Error("prompt does not contain suspicious exe path")
	}
}

func TestBuildSystemOverviewPrompt_ValidJSON(t *testing.T) {
	metrics := collector.SystemMetrics{
		CPUTotalPercent: 30.0,
		MemPercent:      50.0,
		MemTotal:        8 * 1024 * 1024 * 1024,
		MemUsed:         4 * 1024 * 1024 * 1024,
	}

	procs := []collector.ProcessInfo{
		{PID: 1, Name: "systemd", CPUPercent: 0.1, MemPercent: 0.5},
		{PID: 999, Name: "xmrig", CPUPercent: 95.0, MemPercent: 3.0,
			Anomalies: []collector.AnomalyFlag{{Severity: "medium", Reason: "name matches suspicious pattern"}}},
	}

	req := llm.BuildSystemOverviewPrompt(metrics, procs, "llama3.2")

	// Extract the JSON block embedded in the prompt
	start := strings.Index(req.Prompt, "{")
	end := strings.LastIndex(req.Prompt, "}") + 1
	if start < 0 || end <= start {
		t.Fatal("could not locate JSON block in prompt")
	}
	jsonBlock := req.Prompt[start:end]

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonBlock), &parsed); err != nil {
		t.Fatalf("embedded JSON is invalid: %v\njson: %s", err, jsonBlock)
	}

	if _, ok := parsed["system_load"]; !ok {
		t.Error("JSON missing 'system_load' key")
	}
	if _, ok := parsed["top_processes"]; !ok {
		t.Error("JSON missing 'top_processes' key")
	}
}

func TestBuildAnomalyExplanationPrompt(t *testing.T) {
	proc := collector.ProcessInfo{
		PID:  555,
		Name: "suspectd",
		Exe:  "/tmp/suspectd",
	}
	anomaly := collector.AnomalyFlag{
		Severity: "high",
		Reason:   "executable in suspicious path: /tmp/suspectd",
	}

	req := llm.BuildAnomalyExplanationPrompt(proc, anomaly, "mistral")

	if !strings.Contains(req.Prompt, "suspicious path") {
		t.Error("anomaly reason not included in prompt")
	}
	if !strings.Contains(req.Prompt, "high") {
		t.Error("anomaly severity not included in prompt")
	}
}

// ─── Client construction test (no network) ────────────────────────────────────

func TestNewClient_Defaults(t *testing.T) {
	c := llm.NewClient("http://localhost:11434", "llama3.2", nil, 30*1000*1000*1000)
	if c.ActiveModel() != "llama3.2" {
		t.Errorf("unexpected active model: %s", c.ActiveModel())
	}
}
