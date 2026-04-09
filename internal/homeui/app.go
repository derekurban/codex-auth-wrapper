package homeui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
)

type ActionType string

const (
	ActionQuit       ActionType = "quit"
	ActionContinue   ActionType = "continue"
	ActionAddProfile ActionType = "add_profile"
)

type Action struct {
	Type        ActionType
	ProfileName string
	ProfileID   string
}

type screen string

const (
	screenHome screen = "home"
	screenAdd  screen = "add"
)

type Model struct {
	client        *ipc.Client
	sessionID     string
	screen        screen
	width         int
	height        int
	selectedIndex int
	snapshot      ipc.HomeSnapshotResponse
	statusMessage string
	errMessage    string
	action        Action
	nameInput     textinput.Model
	idInput       textinput.Model
	focusIndex    int
	idDirty       bool
}

type snapshotMsg struct {
	snapshot ipc.HomeSnapshotResponse
	err      error
}

func Run(client *ipc.Client, sessionID string, statusMessage string) (Action, error) {
	nameInput := textinput.New()
	nameInput.Placeholder = "Personal 1"
	nameInput.CharLimit = 48
	nameInput.Width = 30
	idInput := textinput.New()
	idInput.Placeholder = "personal-1"
	idInput.CharLimit = 48
	idInput.Width = 30

	model := Model{
		client:        client,
		sessionID:     sessionID,
		screen:        screenHome,
		statusMessage: statusMessage,
		nameInput:     nameInput,
		idInput:       idInput,
	}
	p := tea.NewProgram(model, tea.WithAltScreen())
	result, err := p.Run()
	if err != nil {
		return Action{}, err
	}
	finalModel := result.(Model)
	return finalModel.action, nil
}

func (m Model) Init() tea.Cmd {
	return m.refreshCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case snapshotMsg:
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
		m.snapshot = msg.snapshot
		if len(m.snapshot.Profiles) == 0 {
			m.selectedIndex = 0
		} else if m.selectedIndex >= len(m.snapshot.Profiles) {
			m.selectedIndex = len(m.snapshot.Profiles) - 1
		}
		return m, nil
	case tea.KeyMsg:
		switch m.screen {
		case screenHome:
			return m.updateHome(msg)
		case screenAdd:
			return m.updateAdd(msg)
		}
	}
	return m, nil
}

func (m Model) updateHome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.action = Action{Type: ActionQuit}
		return m, tea.Quit
	case "r":
		return m, m.refreshCmd()
	case "a":
		m.screen = screenAdd
		m.focusIndex = 0
		m.idDirty = false
		m.nameInput.SetValue("")
		m.idInput.SetValue("")
		m.nameInput.Focus()
		m.idInput.Blur()
		return m, nil
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		return m, nil
	case "down", "j":
		if m.selectedIndex < len(m.snapshot.Profiles)-1 {
			m.selectedIndex++
		}
		return m, nil
	case " ":
		if len(m.snapshot.Profiles) == 0 {
			return m, nil
		}
		profile := m.snapshot.Profiles[m.selectedIndex]
		if profile.Selected {
			return m, nil
		}
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := m.client.Request(ctx, "profile.select", ipc.SelectProfileRequest{
				SessionID: m.sessionID,
				ProfileID: profile.ID,
			}, nil); err != nil {
				return snapshotMsg{err: err}
			}
			return loadSnapshot(m.client, m.sessionID)
		}
	case "enter":
		if len(m.snapshot.Profiles) == 0 {
			m.screen = screenAdd
			m.focusIndex = 0
			m.idDirty = false
			m.nameInput.SetValue("")
			m.idInput.SetValue("")
			m.nameInput.Focus()
			m.idInput.Blur()
			return m, nil
		}
		m.action = Action{Type: ActionContinue}
		return m, tea.Quit
	}
	return m, nil
}

func (m Model) updateAdd(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.action = Action{Type: ActionQuit}
		return m, tea.Quit
	case "esc":
		m.screen = screenHome
		m.errMessage = ""
		return m, m.refreshCmd()
	case "tab", "shift+tab", "up", "down":
		if m.focusIndex == 0 {
			m.focusIndex = 1
			m.nameInput.Blur()
			m.idInput.Focus()
		} else {
			m.focusIndex = 0
			m.idInput.Blur()
			m.nameInput.Focus()
		}
		return m, nil
	case "enter":
		name := strings.TrimSpace(m.nameInput.Value())
		id := strings.TrimSpace(m.idInput.Value())
		if name == "" || id == "" {
			m.errMessage = "Profile name and ID are required."
			return m, nil
		}
		m.action = Action{
			Type:        ActionAddProfile,
			ProfileName: name,
			ProfileID:   id,
		}
		return m, tea.Quit
	}
	var cmd tea.Cmd
	if m.focusIndex == 0 {
		m.nameInput, cmd = m.nameInput.Update(msg)
		if !m.idDirty {
			m.idInput.SetValue(codex.Slugify(m.nameInput.Value()))
		}
		return m, cmd
	}
	before := m.idInput.Value()
	m.idInput, cmd = m.idInput.Update(msg)
	m.idDirty = m.idDirty || m.idInput.Value() != before || strings.TrimSpace(m.idInput.Value()) != codex.Slugify(m.nameInput.Value())
	return m, cmd
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading Codex Auth Wrapper..."
	}
	bg := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F7F1E8")).
		Background(lipgloss.Color("#11151C"))
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F6AE2D")).
		PaddingBottom(1).
		Render("Codex Auth Wrapper")
	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9DB4C0")).
		Render("One shared Codex runtime. One selected auth context. Multiple wrapper sessions.")
	var body string
	switch m.screen {
	case screenAdd:
		body = m.viewAdd()
	default:
		body = m.viewHome()
	}
	return bg.
		Width(m.width).
		Height(m.height).
		Padding(1, 3).
		Render(lipgloss.JoinVertical(lipgloss.Left, title, subtitle, "", body))
}

func (m Model) viewHome() string {
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color("#7BD389"))
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0"))
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A3B47")).
		Padding(1, 2).
		Width(max(60, m.width-10))

	primaryLine := "Press Enter to set up your first account"
	if len(m.snapshot.Profiles) > 0 {
		primaryLine = "Press Enter to continue into Codex"
	}
	header := accent.Render(primaryLine)
	status := ""
	if m.statusMessage != "" {
		status = muted.Render(m.statusMessage)
	}
	if m.errMessage != "" {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render(m.errMessage)
	}
	if m.snapshot.DegradedReason != nil {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("Degraded: " + *m.snapshot.DegradedReason)
	}
	rows := []string{}
	if len(m.snapshot.Profiles) == 0 {
		rows = append(rows, muted.Render("No accounts linked yet. Add one to start using Codex through the wrapper."))
	} else {
		for i, profile := range m.snapshot.Profiles {
			marker := "  "
			if profile.Selected {
				marker = "• "
			}
			cursor := " "
			if i == m.selectedIndex {
				cursor = ">"
			}
			rows = append(rows, formatProfileRow(cursor, marker, profile))
		}
	}
	keys := muted.Render("Keys: Enter continue  a add account  space select account  r refresh  q quit")
	parts := []string{header}
	if status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, strings.Join(rows, "\n"), keys)
	return panel.Render(strings.Join(parts, "\n\n"))
}

func (m Model) viewAdd() string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0"))
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A3B47")).
		Padding(1, 2).
		Width(max(60, m.width-10))
	body := []string{
		"Link a Codex account",
		"",
		"Profile name",
		m.nameInput.View(),
		"",
		"Profile ID",
		m.idInput.View(),
		"",
		muted.Render("Enter confirms and hands off to stock `codex login`. Esc returns home."),
	}
	if m.errMessage != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render(m.errMessage))
	}
	return panel.Render(strings.Join(body, "\n"))
}

func formatProfileRow(cursor, marker string, profile ipc.ProfileSummary) string {
	nameStyle := lipgloss.NewStyle().Bold(profile.Selected)
	status := string(profile.Health)
	usage := []string{}
	if profile.FiveHourUsagePercent != nil {
		usage = append(usage, fmt.Sprintf("primary %d%%", *profile.FiveHourUsagePercent))
	}
	if profile.WeeklyUsagePercent != nil {
		usage = append(usage, fmt.Sprintf("secondary %d%%", *profile.WeeklyUsagePercent))
	}
	usageText := "usage unknown"
	if len(usage) > 0 {
		usageText = strings.Join(usage, "  ")
	}
	return fmt.Sprintf("%s %s%s  [%s]  %s", cursor, marker, nameStyle.Render(profile.Name), status, usageText)
}

func (m Model) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		return loadSnapshot(m.client, m.sessionID)
	}
}

func loadSnapshot(client *ipc.Client, sessionID string) snapshotMsg {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var snapshot ipc.HomeSnapshotResponse
	err := client.Request(ctx, "home.snapshot", ipc.HomeSnapshotRequest{SessionID: sessionID}, &snapshot)
	return snapshotMsg{snapshot: snapshot, err: err}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
