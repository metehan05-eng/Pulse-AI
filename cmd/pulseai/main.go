package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"

	"github.com/spectre05/pulseai/internal/collector"
	"github.com/spectre05/pulseai/internal/llm"
	"github.com/spectre05/pulseai/internal/ui"
	"github.com/spectre05/pulseai/pkg/config"
)

var cfg = config.DefaultConfig()

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// ─── Root command (launches TUI) ─────────────────────────────────────────────

var rootCmd = &cobra.Command{
	Use:   "pulse",
	Short: "PulseAI — System Telemetry & AI Security Agent",
	Long: `PulseAI is a production-grade terminal dashboard that combines real-time
system telemetry with AI-powered security analysis via a local Ollama LLM.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runTUI()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVar(&cfg.OllamaBaseURL, "ollama-url", cfg.OllamaBaseURL, "Ollama base URL")
	rootCmd.PersistentFlags().StringVar(&cfg.DefaultModel, "model", cfg.DefaultModel, "Ollama model name")

	rootCmd.AddCommand(analyzeCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(modelsCmd)
}

// ─── TUI runner ──────────────────────────────────────────────────────────────

func runTUI() error {
	mc := collector.NewMetricsCollector(60)
	pc := collector.NewProcessCollector(
		cfg.TopProcessCount,
		cfg.AnomalyThresholds.CPUPercent,
		cfg.AnomalyThresholds.MemoryPercent,
	)
	llmClient := llm.NewClient(
		cfg.OllamaBaseURL,
		cfg.DefaultModel,
		cfg.FallbackModels,
		cfg.LLMTimeout,
	)

	// Non-fatal: if Ollama is unavailable the TUI still shows metrics
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := llmClient.ProbeModels(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "⚠  Ollama not available: %v\n    AI features will be disabled.\n\n", err)
	}

	m := ui.New(cfg, mc, pc, llmClient)

	p := tea.NewProgram(
		m,
		tea.WithAltScreen(),
		tea.WithMouseCellMotion(),
	)
	_, err := p.Run()
	return err
}

// ─── `pulse analyze` ─────────────────────────────────────────────────────────

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyse a process with AI (CLI mode, no TUI)",
	Example: `  pulse analyze --pid 1234
  pulse analyze --pid 1234 --json`,
	RunE: runAnalyze,
}

var (
	analyzePID  int
	analyzeJSON bool
)

func init() {
	analyzeCmd.Flags().IntVar(&analyzePID, "pid", 0, "PID of the process to analyse (required)")
	_ = analyzeCmd.MarkFlagRequired("pid")
	analyzeCmd.Flags().BoolVar(&analyzeJSON, "json", false, "Output result as JSON")
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	mc := collector.NewMetricsCollector(5)
	pc := collector.NewProcessCollector(20, 0, 0)

	fmt.Fprintln(os.Stderr, "⏳ Collecting metrics…")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := mc.Collect(ctx); err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	if err := pc.Collect(ctx); err != nil {
		return fmt.Errorf("processes: %w", err)
	}

	proc, ok := pc.GetByPID(int32(analyzePID))
	if !ok {
		// PID not in top-N; do a direct lookup
		all, err := collector.GetProcessByPID(int32(analyzePID))
		if err != nil {
			return fmt.Errorf("process %d not found: %w", analyzePID, err)
		}
		proc = all
	}

	llmClient := llm.NewClient(
		cfg.OllamaBaseURL,
		cfg.DefaultModel,
		cfg.FallbackModels,
		cfg.LLMTimeout,
	)
	if err := llmClient.ProbeModels(context.Background()); err != nil {
		return fmt.Errorf("Ollama unavailable: %w", err)
	}

	req := llm.BuildProcessAnalysisPrompt(proc, mc.Latest(), pc.Latest(), llmClient.ActiveModel())

	if analyzeJSON {
		type jsonOut struct {
			PID     int32  `json:"pid"`
			Name    string `json:"name"`
			Model   string `json:"model"`
			Analysis string `json:"analysis"`
		}
		var sb string
		err := llmClient.GenerateStream(context.Background(), req, func(t string) {
			sb += t
		})
		if err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(jsonOut{PID: proc.PID, Name: proc.Name, Model: llmClient.ActiveModel(), Analysis: sb})
	}

	fmt.Printf("\n🔍 Analysing PID %d (%s) with %s\n\n", proc.PID, proc.Name, llmClient.ActiveModel())
	return llmClient.GenerateStream(context.Background(), req, func(t string) {
		fmt.Print(t)
	})
}

// ─── `pulse status` ──────────────────────────────────────────────────────────

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Print a one-shot system telemetry snapshot",
	RunE: func(cmd *cobra.Command, args []string) error {
		mc := collector.NewMetricsCollector(1)
		pc := collector.NewProcessCollector(10, 0, 0)

		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if err := mc.Collect(ctx); err != nil {
			return err
		}
		if err := pc.Collect(ctx); err != nil {
			return err
		}

		m := mc.Latest()
		fmt.Printf("CPU:    %.1f%%\n", m.CPUTotalPercent)
		fmt.Printf("Memory: %.1f%% (%.1fGB / %.1fGB)\n",
			m.MemPercent,
			float64(m.MemUsed)/1e9,
			float64(m.MemTotal)/1e9,
		)
		fmt.Printf("Disk:   %.1f%%\n", m.DiskPercent)
		fmt.Printf("Net↑:   %s/s   Net↓: %s/s\n",
			collector.FormatBytes(m.NetBytesSent),
			collector.FormatBytes(m.NetBytesRecv),
		)
		fmt.Println("\nTop Processes:")
		for _, p := range pc.Latest() {
			flag := ""
			if p.HasAnomalies() {
				flag = " ⚠ " + p.MaxSeverity()
			}
			fmt.Printf("  %-6d %-20s CPU:%.1f%% MEM:%.1f%%%s\n",
				p.PID, p.Name, p.CPUPercent, p.MemPercent, flag)
		}
		return nil
	},
}

// ─── `pulse models` ──────────────────────────────────────────────────────────

var modelsCmd = &cobra.Command{
	Use:   "models",
	Short: "List models available on the local Ollama server",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Ping via ProbeModels then report
		llmClient := llm.NewClient(cfg.OllamaBaseURL, cfg.DefaultModel, cfg.FallbackModels, 10*time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := llmClient.ProbeModels(ctx); err != nil {
			return err
		}
		fmt.Printf("Active model: %s\n", llmClient.ActiveModel())
		return nil
	},
}

// ─── helpers ─────────────────────────────────────────────────────────────────

var _ = strconv.Itoa // keep import used if needed
