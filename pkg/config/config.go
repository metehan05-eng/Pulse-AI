package config

import (
	"os"
	"time"
)

// Config holds all runtime configuration for PulseAI.
type Config struct {
	// Ollama settings
	OllamaBaseURL    string
	DefaultModel     string
	FallbackModels   []string
	LLMTimeout       time.Duration
	StreamingEnabled bool

	// Collector settings
	MetricsInterval  time.Duration
	TopProcessCount  int
	AnomalyThresholds AnomalyThresholds

	// UI settings
	RefreshRate time.Duration
}

// AnomalyThresholds defines thresholds for flagging suspicious behaviour.
type AnomalyThresholds struct {
	CPUPercent    float64 // per-process CPU % above which it is flagged
	MemoryPercent float64 // per-process memory % above which it is flagged
}

// DefaultConfig returns a sensible production default configuration.
func DefaultConfig() *Config {
	ollamaURL := os.Getenv("OLLAMA_BASE_URL")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	model := os.Getenv("PULSEAI_MODEL")
	if model == "" {
		model = "qwen2.5-coder"
	}

	return &Config{
		OllamaBaseURL:    ollamaURL,
		DefaultModel:     model,
		FallbackModels:   []string{"llama3.2", "mistral", "gemma2"},
		LLMTimeout:       120 * time.Second,
		StreamingEnabled: true,

		MetricsInterval: 2 * time.Second,
		TopProcessCount: 10,
		AnomalyThresholds: AnomalyThresholds{
			CPUPercent:    80.0,
			MemoryPercent: 5.0,
		},

		RefreshRate: 1 * time.Second,
	}
}
