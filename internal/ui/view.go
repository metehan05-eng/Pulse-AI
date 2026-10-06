package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/spectre05/pulseai/internal/collector"
)

// ─── Design tokens ───────────────────────────────────────────────────────────

var (
	// Palette
	colorPrimary   = lipgloss.Color("#7C3AED") // violet
	colorAccent    = lipgloss.Color("#06B6D4") // cyan
	colorSuccess   = lipgloss.Color("#10B981") // green
	colorWarning   = lipgloss.Color("#F59E0B") // amber
	colorDanger    = lipgloss.Color("#EF4444") // red
	colorMuted     = lipgloss.Color("#6B7280") // gray
	colorText      = lipgloss.Color("#F9FAFB") // near-white
	colorBg        = lipgloss.Color("#111827") // dark
	colorPaneBg    = lipgloss.Color("#1F2937") // slightly lighter
	colorBorder    = lipgloss.Color("#374151")
	colorBorderFoc = lipgloss.Color("#7C3AED")

	// Styles
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorPrimary).
			Background(colorBg).
			Padding(0, 1)

	paneLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(colorAccent).
			MarginBottom(1)

	paneStyle = lipgloss.NewStyle().
			Background(colorPaneBg).
			Border(lipgloss.RoundedBorder()).
			BorderForeground(colorBorder).
			Padding(0, 1)

	paneStyleFocused = paneStyle.Copy().
				BorderForeground(colorBorderFoc)

	helpStyle = lipgloss.NewStyle().
			Foreground(colorMuted).
			Italic(true)

	anomalyHighStyle = lipgloss.NewStyle().
				Foreground(colorDanger).Bold(true)
	anomalyMedStyle = lipgloss.NewStyle().
				Foreground(colorWarning).Bold(true)
	anomalyLowStyle = lipgloss.NewStyle().
				Foreground(colorAccent)

	valueStyle   = lipgloss.NewStyle().Foreground(colorText)
	labelStyle   = lipgloss.NewStyle().Foreground(colorMuted)
	successStyle = lipgloss.NewStyle().Foreground(colorSuccess)
)

// ─── View ────────────────────────────────────────────────────────────────────

func (m Model) View() string {
	if m.quitting {
		return titleStyle.Render("👋  PulseAI — Goodbye!") + "\n"
	}
	if m.width == 0 {
		return "Loading…"
	}

	// Compute column widths
	rightW := aiPaneWidth(m.width)
	leftW := m.width - rightW - 4 // 4 = padding/borders

	top := m.renderHeader()
	leftCol := lipgloss.JoinVertical(lipgloss.Left,
		m.renderMetricsPane(leftW),
		m.renderProcessPane(leftW),
	)
	rightCol := m.renderAIPane(rightW)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftCol, rightCol)
	footer := m.renderFooter()

	return lipgloss.JoinVertical(lipgloss.Left, top, body, footer)
}

// ─── Header ──────────────────────────────────────────────────────────────────

func (m Model) renderHeader() string {
	model := m.llmCli.ActiveModel()
	refresh := m.lastRefresh.Format("15:04:05")

	left := titleStyle.Render("⚡ PulseAI")
	right := helpStyle.Render(fmt.Sprintf("model: %s  |  refreshed: %s", model, refresh))

	spacer := strings.Repeat(" ", max(0, m.width-lipgloss.Width(left)-lipgloss.Width(right)-2))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, spacer, right) + "\n"
}

// ─── Metrics Pane ────────────────────────────────────────────────────────────

func (m Model) renderMetricsPane(width int) string {
	label := paneLabelStyle.Render("󰊓  System Metrics")

	met := m.metrics
	content := strings.Builder{}
	content.WriteString(label + "\n")

	// CPU
	content.WriteString(renderGauge("CPU Total", met.CPUTotalPercent, 100, width-6))
	content.WriteString(renderCPUCores(met.CPUPerCore, width-6))

	// Memory
	usedGB := float64(met.MemUsed) / 1e9
	totalGB := float64(met.MemTotal) / 1e9
	content.WriteString(renderGauge(
		fmt.Sprintf("Memory  %.1f/%.1fGB", usedGB, totalGB),
		met.MemPercent, 100, width-6,
	))

	// Swap
	if met.SwapTotal > 0 {
		swapGB := float64(met.SwapUsed) / 1e9
		swapTotalGB := float64(met.SwapTotal) / 1e9
		content.WriteString(renderGauge(
			fmt.Sprintf("Swap    %.1f/%.1fGB", swapGB, swapTotalGB),
			met.SwapPercent, 100, width-6,
		))
	}

	// Disk
	content.WriteString(renderGauge(
		fmt.Sprintf("Disk    %s/%s", collector.FormatBytes(met.DiskUsed), collector.FormatBytes(met.DiskTotal)),
		met.DiskPercent, 100, width-6,
	))

	// Network
	content.WriteString(fmt.Sprintf(
		"%s ↑ %s/s  ↓ %s/s\n",
		labelStyle.Render("Network "),
		formatRate(met.NetBytesSent, m.cfg.RefreshRate),
		formatRate(met.NetBytesRecv, m.cfg.RefreshRate),
	))

	// Sparkline
	spark := m.mc.CPUSparkline(width - 10)
	if len(spark) > 0 {
		content.WriteString(labelStyle.Render("CPU Trend ") + renderSparkline(spark) + "\n")
	}

	inner := content.String()
	style := paneStyle
	if m.focused == paneMetrics {
		style = paneStyleFocused
	}
	return style.Width(width).Render(inner) + "\n"
}

// renderGauge draws a percentage bar with colour coding.
func renderGauge(label string, value, max float64, width int) string {
	pct := value / max
	barWidth := width - 12
	if barWidth < 4 {
		barWidth = 4
	}
	filled := int(math.Round(pct * float64(barWidth)))
	if filled > barWidth {
		filled = barWidth
	}

	var barColor lipgloss.Color
	switch {
	case pct >= 0.85:
		barColor = colorDanger
	case pct >= 0.65:
		barColor = colorWarning
	default:
		barColor = colorSuccess
	}

	bar := lipgloss.NewStyle().Foreground(barColor).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(colorBorder).Render(strings.Repeat("░", barWidth-filled))

	pctStr := lipgloss.NewStyle().Foreground(barColor).Bold(true).Render(fmt.Sprintf("%5.1f%%", value))
	return fmt.Sprintf("%s %s %s\n",
		labelStyle.Render(truncLabel(label, 10)),
		bar,
		pctStr,
	)
}

// renderCPUCores renders a compact per-core strip.
func renderCPUCores(cores []float64, width int) string {
	if len(cores) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(labelStyle.Render("Cores   "))
	for i, c := range cores {
		if i >= 16 { // cap visual at 16 cores
			sb.WriteString(labelStyle.Render("…"))
			break
		}
		var col lipgloss.Color
		switch {
		case c >= 85:
			col = colorDanger
		case c >= 65:
			col = colorWarning
		default:
			col = colorSuccess
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(col).Render("▪"))
	}
	sb.WriteString("\n")
	return sb.String()
}

// renderSparkline renders a UTF-8 sparkline from float64 values.
func renderSparkline(values []float64) string {
	const sparks = "▁▂▃▄▅▆▇█"
	runes := []rune(sparks)
	n := float64(len(runes) - 1)

	var sb strings.Builder
	for _, v := range values {
		idx := int(math.Round((v / 100.0) * n))
		if idx >= len(runes) {
			idx = len(runes) - 1
		}
		if idx < 0 {
			idx = 0
		}
		var col lipgloss.Color
		switch {
		case v >= 85:
			col = colorDanger
		case v >= 65:
			col = colorWarning
		default:
			col = colorSuccess
		}
		sb.WriteString(lipgloss.NewStyle().Foreground(col).Render(string(runes[idx])))
	}
	return sb.String()
}

// ─── Process Pane ────────────────────────────────────────────────────────────

func (m Model) renderProcessPane(width int) string {
	label := paneLabelStyle.Render("󰍛  Processes")

	header := fmt.Sprintf("%-6s %-16s %-8s %-8s %-8s %s\n",
		labelStyle.Render("PID"),
		labelStyle.Render("Name"),
		labelStyle.Render("CPU%"),
		labelStyle.Render("MEM%"),
		labelStyle.Render("Threads"),
		labelStyle.Render("Flags"),
	)

	var rows strings.Builder
	rows.WriteString(label + "\n")
	rows.WriteString(header)
	rows.WriteString(strings.Repeat("─", width-4) + "\n")

	for i, p := range m.processes {
		row := renderProcessRow(p, i == m.selectedProc, width-4)
		rows.WriteString(row)
	}

	style := paneStyle
	if m.focused == paneProcess {
		style = paneStyleFocused
	}
	return style.Width(width).Render(rows.String())
}

func renderProcessRow(p collector.ProcessInfo, selected bool, width int) string {
	flags := renderAnomalyBadges(p)

	pid := fmt.Sprintf("%-6d", p.PID)
	name := truncLabel(p.Name, 16)
	cpu := fmt.Sprintf("%-8.1f", p.CPUPercent)
	mem := fmt.Sprintf("%-8.1f", p.MemPercent)
	thr := fmt.Sprintf("%-8d", p.NumThreads)

	row := fmt.Sprintf("%-6s %-16s %-8s %-8s %-8s %s",
		pid, name, cpu, mem, thr, flags)

	if selected {
		return lipgloss.NewStyle().
			Background(colorPrimary).
			Foreground(colorText).
			Bold(true).
			Width(width).
			Render(row) + "\n"
	}

	if p.HasAnomalies() {
		sev := p.MaxSeverity()
		switch sev {
		case "high":
			return anomalyHighStyle.Width(width).Render(row) + "\n"
		case "medium":
			return anomalyMedStyle.Width(width).Render(row) + "\n"
		default:
			return anomalyLowStyle.Width(width).Render(row) + "\n"
		}
	}

	return valueStyle.Width(width).Render(row) + "\n"
}

func renderAnomalyBadges(p collector.ProcessInfo) string {
	if !p.HasAnomalies() {
		return successStyle.Render("●")
	}
	switch p.MaxSeverity() {
	case "high":
		return anomalyHighStyle.Render("▲ HIGH")
	case "medium":
		return anomalyMedStyle.Render("◆ MED")
	default:
		return anomalyLowStyle.Render("▪ LOW")
	}
}

// ─── AI Pane ─────────────────────────────────────────────────────────────────

func (m Model) renderAIPane(width int) string {
	var label string
	if m.aiStreaming {
		label = paneLabelStyle.Render(fmt.Sprintf("󰧑  AI Analysis  %s", m.spinner.View()))
	} else {
		label = paneLabelStyle.Render("󰧑  AI Analysis")
	}

	content := label + "\n"
	if m.aiResponse == "" && !m.aiStreaming {
		content += helpStyle.Render("Press 'a' to analyse selected process\nPress 'g' for system overview")
	} else {
		content += m.aiPane.View()
	}

	style := paneStyle
	if m.focused == paneAI {
		style = paneStyleFocused
	}
	return style.Width(width).Height(aiPaneHeight(m.height)).Render(content)
}

// ─── Footer ──────────────────────────────────────────────────────────────────

func (m Model) renderFooter() string {
	keys := helpStyle.Render(
		"tab focus  j/k navigate  a analyze  g overview  r refresh  q quit",
	)
	if m.statusMsg != "" {
		keys = helpStyle.Render(m.statusMsg+"  |  ") + keys
	}
	uptime := time.Since(m.lastRefresh).Round(time.Second)
	right := helpStyle.Render(fmt.Sprintf("last refresh: %s ago", uptime))
	spacer := strings.Repeat(" ", max(0, m.width-lipgloss.Width(keys)-lipgloss.Width(right)-2))
	return "\n" + lipgloss.JoinHorizontal(lipgloss.Top, keys, spacer, right)
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func truncLabel(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func formatRate(bytes uint64, interval time.Duration) string {
	if interval <= 0 {
		interval = time.Second
	}
	bps := float64(bytes) / interval.Seconds()
	return collector.FormatBytes(uint64(bps))
}
