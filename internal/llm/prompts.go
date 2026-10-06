package llm

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spectre05/pulseai/internal/collector"
)

const systemPrompt = `You are PulseAI, an expert Linux systems analyst and cybersecurity specialist embedded directly in a sysadmin's terminal.

Your task is to analyse system telemetry and process data, then produce clear, structured, actionable insights.

Guidelines:
- Be concise but thorough. Use bullet points and short paragraphs.
- Lead with severity: CRITICAL → HIGH → MEDIUM → LOW → INFO.
- Provide concrete, shell-friendly remediation steps where applicable.
- Never hallucinate data — only reason about what is provided in the context.
- Format output for a terminal: no markdown headers larger than ##, prefer plain text bullets.`

// ─── Context payload types ───────────────────────────────────────────────────

// ProcessContext is the JSON payload sent to the LLM for process analysis.
type ProcessContext struct {
	Timestamp    string                   `json:"timestamp"`
	SystemLoad   SystemLoadSnapshot       `json:"system_load"`
	TargetProcess ProcessPayload          `json:"target_process"`
	TopProcesses []ProcessPayload         `json:"top_processes"`
}

// SystemLoadSnapshot summarises overall machine health.
type SystemLoadSnapshot struct {
	CPUPercent  float64 `json:"cpu_percent"`
	MemPercent  float64 `json:"mem_percent"`
	MemUsedGB   float64 `json:"mem_used_gb"`
	MemTotalGB  float64 `json:"mem_total_gb"`
	DiskPercent float64 `json:"disk_percent"`
	NetSentMBps float64 `json:"net_sent_mbps"`
	NetRecvMBps float64 `json:"net_recv_mbps"`
}

// ProcessPayload is the per-process fragment.
type ProcessPayload struct {
	PID        int32    `json:"pid"`
	Name       string   `json:"name"`
	Username   string   `json:"username"`
	Exe        string   `json:"exe"`
	CmdLine    string   `json:"cmdline"`
	CPUPercent float64  `json:"cpu_percent"`
	MemPercent float32  `json:"mem_percent"`
	MemRSSMB   float64  `json:"mem_rss_mb"`
	NumThreads int32    `json:"num_threads"`
	Status     string   `json:"status"`
	OpenFiles  int      `json:"open_files"`
	Anomalies  []string `json:"anomalies,omitempty"`
}

// ─── Prompt builders ─────────────────────────────────────────────────────────

// BuildProcessAnalysisPrompt returns a GenerateRequest ready to send to Ollama.
// It serialises the target process and top-N list into a structured JSON context.
func BuildProcessAnalysisPrompt(
	target collector.ProcessInfo,
	metrics collector.SystemMetrics,
	topProcs []collector.ProcessInfo,
	model string,
) GenerateRequest {
	ctx := buildContext(target, metrics, topProcs)
	ctxJSON, _ := json.MarshalIndent(ctx, "", "  ")

	prompt := fmt.Sprintf(`Analyse the following system telemetry snapshot and the selected process.

<system_context>
%s
</system_context>

Tasks:
1. Identify whether the selected process is behaving normally.
2. Evaluate any anomaly flags and explain what each means.
3. Assess overall system health based on CPU/memory/disk metrics.
4. List potential security or performance risks.
5. Provide concrete remediation steps.

Respond in plain terminal-friendly text.`, string(ctxJSON))

	return GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: systemPrompt,
		Stream: true,
	}
}

// BuildSystemOverviewPrompt generates a high-level system health assessment.
func BuildSystemOverviewPrompt(
	metrics collector.SystemMetrics,
	topProcs []collector.ProcessInfo,
	model string,
) GenerateRequest {
	ctx := buildContext(collector.ProcessInfo{}, metrics, topProcs)
	ctxJSON, _ := json.MarshalIndent(ctx, "", "  ")

	prompt := fmt.Sprintf(`Provide a concise system health overview based on this telemetry:

<system_context>
%s
</system_context>

Include:
1. Overall health rating (Healthy / Warning / Critical).
2. Top resource consumers and why they matter.
3. Any anomalous processes and their risk level.
4. Recommended immediate actions (if any).

Keep the response under 300 words, terminal-friendly.`, string(ctxJSON))

	return GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: systemPrompt,
		Stream: true,
	}
}

// BuildAnomalyExplanationPrompt focuses on a single anomaly flag.
func BuildAnomalyExplanationPrompt(
	proc collector.ProcessInfo,
	anomaly collector.AnomalyFlag,
	model string,
) GenerateRequest {
	prompt := fmt.Sprintf(`A running process has been flagged with the following anomaly:

Process: %s (PID %d)
Executable: %s
Command: %s
Anomaly severity: %s
Anomaly reason: %s

Explain in 3-5 sentences:
1. What this anomaly typically indicates.
2. Whether this could be benign or malicious, and how to distinguish.
3. The safest immediate action to take.`,
		proc.Name, proc.PID, proc.Exe, proc.CmdLine,
		anomaly.Severity, anomaly.Reason,
	)

	return GenerateRequest{
		Model:  model,
		Prompt: prompt,
		System: systemPrompt,
		Stream: true,
	}
}

// ─── Context builder ─────────────────────────────────────────────────────────

func buildContext(
	target collector.ProcessInfo,
	metrics collector.SystemMetrics,
	topProcs []collector.ProcessInfo,
) ProcessContext {
	load := SystemLoadSnapshot{
		CPUPercent:  metrics.CPUTotalPercent,
		MemPercent:  metrics.MemPercent,
		MemUsedGB:   toGB(metrics.MemUsed),
		MemTotalGB:  toGB(metrics.MemTotal),
		DiskPercent: metrics.DiskPercent,
		NetSentMBps: toMBps(metrics.NetBytesSent),
		NetRecvMBps: toMBps(metrics.NetBytesRecv),
	}

	top := make([]ProcessPayload, 0, len(topProcs))
	for _, p := range topProcs {
		top = append(top, toPayload(p))
	}

	return ProcessContext{
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		SystemLoad:    load,
		TargetProcess: toPayload(target),
		TopProcesses:  top,
	}
}

func toPayload(p collector.ProcessInfo) ProcessPayload {
	var anomalyStrs []string
	for _, a := range p.Anomalies {
		anomalyStrs = append(anomalyStrs, fmt.Sprintf("[%s] %s", strings.ToUpper(a.Severity), a.Reason))
	}
	return ProcessPayload{
		PID:        p.PID,
		Name:       p.Name,
		Username:   p.Username,
		Exe:        p.Exe,
		CmdLine:    truncate(p.CmdLine, 200),
		CPUPercent: p.CPUPercent,
		MemPercent: p.MemPercent,
		MemRSSMB:   float64(p.MemRSS) / 1024 / 1024,
		NumThreads: p.NumThreads,
		Status:     p.Status,
		OpenFiles:  p.OpenFiles,
		Anomalies:  anomalyStrs,
	}
}

func toGB(b uint64) float64  { return float64(b) / 1024 / 1024 / 1024 }
func toMBps(b uint64) float64 { return float64(b) / 1024 / 1024 }

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
