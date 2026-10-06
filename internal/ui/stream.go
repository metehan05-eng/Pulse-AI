package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// updateExtended is called from Update to handle streaming message types
// that require the recursive token-drain pattern.
// We embed this logic directly in Update by checking the type assertion.

// HandleStreamMessages patches the Update method to process streaming messages.
// Call this from within Model.Update after the main switch statement.
func (m Model) handleExtendedMsg(msg tea.Msg) (Model, tea.Cmd) {
	switch msg := msg.(type) {
	case streamStartMsg:
		return m, readNextToken(msg.tokenCh, msg.errCh)

	case tokenBatchMsg:
		m.aiResponse += msg.token
		m.aiPane.SetContent(m.aiResponse)
		m.aiPane.GotoBottom()
		return m, readNextToken(msg.tokenCh, msg.errCh)
	}
	return m, nil
}
