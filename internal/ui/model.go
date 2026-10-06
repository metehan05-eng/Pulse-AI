// Package ui provides the Bubbletea TUI for PulseAI.
// All IO (metrics polling, LLM calls) is driven via tea.Cmd, never blocking
// the Update loop.
package ui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/spectre05/pulseai/internal/collector"
	"github.com/spectre05/pulseai/internal/llm"
	"github.com/spectre05/pulseai/pkg/config"
)

// ─── Message types ───────────────────────────────────────────────────────────

// metricsMsg carries a fresh system snapshot.
type metricsMsg struct {
	metrics collector.SystemMetrics
	procs   []collector.ProcessInfo
}

// llmTokenMsg carries a single streaming token from Ollama.
type llmTokenMsg struct{ token string }

// llmDoneMsg signals the LLM stream finished (nil err == success).
type llmDoneMsg struct{ err error }

// tickMsg is the heartbeat for auto-refresh.
type tickMsg time.Time

// windowSizeMsg is sent once when the terminal reports its size.
type windowSizeMsg struct{ width, height int }

// ─── Focus / pane enumeration ───────────────────────────────────────────────

type pane int

const (
	paneMetrics pane = iota
	paneProcess
	paneAI
	paneCount
)

// ─── Model ───────────────────────────────────────────────────────────────────

// Model is the root Bubbletea model.
type Model struct {
	cfg    *config.Config
	mc     *collector.MetricsCollector
	pc     *collector.ProcessCollector
	llmCli *llm.Client

	// State
	metrics      collector.SystemMetrics
	processes    []collector.ProcessInfo
	selectedProc int // index into processes slice

	aiResponse  string
	aiStreaming bool
	aiError     string

	// UI sub-components
	spinner  spinner.Model
	aiPane   viewport.Model
	focused  pane
	width    int
	height   int

	// Misc
	lastRefresh time.Time
	statusMsg   string
	quitting    bool
}

// New creates the root model. Call Start() to run the program.
func New(cfg *config.Config, mc *collector.MetricsCollector, pc *collector.ProcessCollector, llmCli *llm.Client) Model {
	sp := spinner.New()
	sp.Spinner = spinner.Dot

	return Model{
		cfg:    cfg,
		mc:     mc,
		pc:     pc,
		llmCli: llmCli,

		spinner: sp,
		focused: paneMetrics,
	}
}

// ─── Bubbletea interface ─────────────────────────────────────────────────────

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spinner.Tick,
		m.doTick(),
		m.collectMetrics(),
	)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {

	// ── Window resize ──
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.aiPane = viewport.New(aiPaneWidth(m.width), aiPaneHeight(m.height))
		m.aiPane.SetContent(m.aiResponse)

	// ── Heartbeat tick ──
	case tickMsg:
		cmds = append(cmds, m.doTick(), m.collectMetrics())

	// ── Fresh metrics ──
	case metricsMsg:
		m.metrics = msg.metrics
		m.processes = msg.procs
		m.lastRefresh = time.Now()
		if m.selectedProc >= len(m.processes) {
			m.selectedProc = max(0, len(m.processes)-1)
		}

	// ── LLM streaming ──
	case llmTokenMsg:
		m.aiResponse += msg.token
		m.aiPane.SetContent(m.aiResponse)
		m.aiPane.GotoBottom()

	case llmDoneMsg:
		m.aiStreaming = false
		if msg.err != nil {
			m.aiError = fmt.Sprintf("⚠ LLM error: %v", msg.err)
			m.aiPane.SetContent(m.aiResponse + "\n\n" + m.aiError)
		}
		cmds = append(cmds, m.spinner.Tick) // keep spinner alive

	// ── Spinner ──
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		cmds = append(cmds, cmd)

	// ── Keyboard ──
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			m.quitting = true
			return m, tea.Quit

		case "tab":
			m.focused = (m.focused + 1) % paneCount
			m.statusMsg = focusLabel(m.focused)

		case "j", "down":
			if m.focused == paneProcess && len(m.processes) > 0 {
				m.selectedProc = min(m.selectedProc+1, len(m.processes)-1)
			}

		case "k", "up":
			if m.focused == paneProcess && len(m.processes) > 0 {
				m.selectedProc = max(m.selectedProc-1, 0)
			}

		case "a":
			if !m.aiStreaming && len(m.processes) > 0 {
				m.aiResponse = ""
				m.aiStreaming = true
				m.aiError = ""
				m.focused = paneAI
				cmds = append(cmds, m.streamAnalysis(m.processes[m.selectedProc]))
			}

		case "r":
			cmds = append(cmds, m.collectMetrics())
			m.statusMsg = "Refreshing…"

		case "g":
			if !m.aiStreaming {
				m.aiResponse = ""
				m.aiStreaming = true
				m.aiError = ""
				m.focused = paneAI
				cmds = append(cmds, m.streamOverview())
			}
		}

		// Forward scroll events to AI viewport when focused
		if m.focused == paneAI {
			var cmd tea.Cmd
			m.aiPane, cmd = m.aiPane.Update(msg)
			cmds = append(cmds, cmd)
		}

	// ── Stream pump (recursive token drain) ──
	case streamStartMsg:
		var cmd tea.Cmd
		_, cmd = m.handleStreamStart(msg)
		cmds = append(cmds, cmd)

	case tokenBatchMsg:
		m.aiResponse += msg.token
		m.aiPane.SetContent(m.aiResponse)
		m.aiPane.GotoBottom()
		cmds = append(cmds, readNextToken(msg.tokenCh, msg.errCh))
	}

	return m, tea.Batch(cmds...)
}

// ─── Commands (non-blocking IO) ──────────────────────────────────────────────

func (m Model) doTick() tea.Cmd {
	return tea.Tick(m.cfg.RefreshRate, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m Model) collectMetrics() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = m.mc.Collect(ctx)
		_ = m.pc.Collect(ctx)
		return metricsMsg{
			metrics: m.mc.Latest(),
			procs:   m.pc.Latest(),
		}
	}
}

func (m Model) streamAnalysis(proc collector.ProcessInfo) tea.Cmd {
	return func() tea.Msg {
		// Build a channel-based streaming bridge.
		// We cannot send messages from inside a tea.Cmd directly, so we return
		// a batch of commands — one for each token read via a Go channel.
		tokenCh := make(chan string, 256)
		errCh := make(chan error, 1)

		req := llm.BuildProcessAnalysisPrompt(
			proc,
			m.mc.Latest(),
			m.pc.Latest(),
			m.llmCli.ActiveModel(),
		)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), m.cfg.LLMTimeout)
			defer cancel()
			err := m.llmCli.GenerateStream(ctx, req, func(token string) {
				tokenCh <- token
			})
			close(tokenCh)
			errCh <- err
		}()

		// Drain tokens, yielding messages back to bubbletea.
		return drainToCmd(tokenCh, errCh)
	}
}

func (m Model) streamOverview() tea.Cmd {
	return func() tea.Msg {
		tokenCh := make(chan string, 256)
		errCh := make(chan error, 1)

		req := llm.BuildSystemOverviewPrompt(
			m.mc.Latest(),
			m.pc.Latest(),
			m.llmCli.ActiveModel(),
		)

		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), m.cfg.LLMTimeout)
			defer cancel()
			err := m.llmCli.GenerateStream(ctx, req, func(token string) {
				tokenCh <- token
			})
			close(tokenCh)
			errCh <- err
		}()

		return drainToCmd(tokenCh, errCh)
	}
}

// drainToCmd reads all tokens from tokenCh and produces a batch tea.Cmd.
// This is the canonical pattern for piping goroutine output into bubbletea.
func drainToCmd(tokenCh <-chan string, errCh <-chan error) tea.Msg {
	// We return a synthetic batchMsg — Bubbletea will call each sub-cmd.
	// The trick: return a tea.Cmd that reads the first token and schedules
	// itself recursively until the channel is empty.
	return streamStartMsg{tokenCh: tokenCh, errCh: errCh}
}

// streamStartMsg kicks off the recursive token-drain loop.
type streamStartMsg struct {
	tokenCh <-chan string
	errCh   <-chan error
}

// We handle streamStartMsg in Update by launching the first drain command.
// Add this case to the Update switch:
func init() {} // handled inline via model extension below

// Extend Update to handle streamStartMsg:
func (m Model) handleStreamStart(msg streamStartMsg) (tea.Model, tea.Cmd) {
	return m, readNextToken(msg.tokenCh, msg.errCh)
}

// readNextToken is a recursive tea.Cmd that reads one token at a time,
// keeping the bubbletea event loop free.
func readNextToken(tokenCh <-chan string, errCh <-chan error) tea.Cmd {
	return func() tea.Msg {
		token, ok := <-tokenCh
		if !ok {
			// Channel closed — stream ended
			var err error
			select {
			case err = <-errCh:
			default:
			}
			return llmDoneMsg{err: err}
		}
		// Schedule next read after delivering this token
		return tokenBatchMsg{token: token, tokenCh: tokenCh, errCh: errCh}
	}
}

// tokenBatchMsg carries a token and re-schedules the next read.
type tokenBatchMsg struct {
	token   string
	tokenCh <-chan string
	errCh   <-chan error
}

// ─── helpers ─────────────────────────────────────────────────────────────────

func focusLabel(p pane) string {
	switch p {
	case paneMetrics:
		return "Focus: System Metrics"
	case paneProcess:
		return "Focus: Process Table"
	case paneAI:
		return "Focus: AI Analysis"
	}
	return ""
}

func aiPaneWidth(total int) int {
	w := total / 2
	if w < 40 {
		return 40
	}
	return w
}

func aiPaneHeight(total int) int {
	h := total - 4
	if h < 10 {
		return 10
	}
	return h
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
