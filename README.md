# ⚡ PulseAI

> **Production-grade System Telemetry & AI Security Agent** — built in Go with a Charm TUI dashboard and local Ollama LLM integration.

---

## Features

| Feature | Details |
|---|---|
| 🖥 **Real-time TUI** | Multi-pane Bubbletea dashboard with Lipgloss styling |
| 📊 **System Gauges** | CPU (total + per-core), Memory, Swap, Disk, Network I/O |
| ✨ **CPU Sparkline** | Rolling history visualised as a UTF-8 sparkline |
| 🔍 **Process Table** | Top 10 processes sorted by anomaly severity then CPU |
| 🚨 **Anomaly Detection** | 8 heuristic rules (tmp paths, deleted exes, suspicious names, unusual ports, base64 blobs…) |
| 🤖 **AI Analysis** | Streaming Ollama LLM responses for selected process or system overview |
| ⌨ **CLI Direct Mode** | `pulse status`, `pulse analyze --pid <N>`, `pulse models` |

---

## Requirements

- Go 1.22+
- [Ollama](https://ollama.com) running locally (optional — metrics work without it)
- Default model: `qwen2.5-coder` (falls back to `llama3.2`, `mistral`, `gemma2`)

---

## Quick Start

```bash
# Clone / enter the project
cd PulseAI

# Download dependencies
go mod tidy

# Build the binary
make build

# Launch the interactive TUI
./pulse

# One-shot status snapshot
./pulse status

# AI-analyse a specific PID
./pulse analyze --pid 1234

# Use a different Ollama model
./pulse --model llama3.2

# Use a remote Ollama endpoint
./pulse --ollama-url http://192.168.1.10:11434
```

---

## Keybindings (TUI)

| Key | Action |
|---|---|
| `Tab` | Cycle pane focus |
| `j` / `↓` | Move down in process table |
| `k` / `↑` | Move up in process table |
| `a` | AI-analyse selected process |
| `g` | AI system overview |
| `r` | Force metrics refresh |
| `q` / `Ctrl+C` | Quit |

---

## Project Layout

```
PulseAI/
├── cmd/pulseai/main.go          # Cobra CLI entry point + TUI launcher
├── internal/
│   ├── collector/
│   │   ├── metrics.go           # CPU / RAM / Net / Disk collector
│   │   ├── process.go           # Process inspector + anomaly detection
│   │   └── lookup.go            # Direct PID lookup (CLI mode)
│   ├── llm/
│   │   ├── client.go            # Ollama REST client (streaming)
│   │   └── prompts.go           # Structured JSON prompt builders
│   └── ui/
│       ├── model.go             # Bubbletea Model (Update loop)
│       ├── view.go              # Lipgloss rendering (all panes)
│       └── stream.go            # Recursive token-drain helpers
└── pkg/config/config.go         # Runtime configuration + env overrides
```

---

## Environment Variables

| Variable | Default | Description |
|---|---|---|
| `OLLAMA_BASE_URL` | `http://localhost:11434` | Ollama server address |
| `PULSEAI_MODEL` | `qwen2.5-coder` | Preferred model name |

---

## Anomaly Detection Rules

| Rule | Severity |
|---|---|
| Executable in `/tmp`, `/dev/shm`, or hidden path | 🔴 HIGH |
| Executable deleted from disk while running | 🔴 HIGH |
| Command line contains embedded base64 blob | 🔴 HIGH |
| Listening on non-standard port | 🟡 MEDIUM |
| Suspicious process name (xmrig, kinsing, masscan…) | 🟡 MEDIUM |
| >500 open file descriptors | 🟡 MEDIUM |
| Executable outside standard system paths | 🟢 LOW |
| >200 threads | 🟢 LOW |

---

## Running Tests

```bash
make test
# or
go test ./internal/... -v
```

---

## License

MIT
