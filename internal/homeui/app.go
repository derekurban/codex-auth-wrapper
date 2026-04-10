package homeui

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/derekurban/codex-auth-wrapper/internal/codex"
	"github.com/derekurban/codex-auth-wrapper/internal/ipc"
)

const (
	defaultRefreshInterval = 45 * time.Second
	warningRefreshInterval = 15 * time.Second
	backgroundRefreshTick  = 2 * time.Second
	externalPollInterval   = 1 * time.Second
	pageHorizontalPadding  = 3
	pageVerticalPadding    = 1
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

type ExternalEvent struct {
	Reload *ipc.ReloadNotice
}

type screen string

const (
	screenHome     screen = "home"
	screenAdd      screen = "add"
	screenSettings screen = "settings"
)

type Model struct {
	client        *ipc.Client
	pollExternal  func() *ExternalEvent
	sessionID     string
	screen        screen
	width         int
	height        int
	selectedIndex int
	scrollOffset  int
	snapshot      ipc.HomeSnapshotResponse
	hasSnapshot   bool
	isLoading     bool
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

type settingsUpdatedMsg struct {
	value bool
	err   error
}

type refreshTickMsg time.Time
type externalPollTickMsg time.Time

func Run(client *ipc.Client, pollExternal func() *ExternalEvent, sessionID string, statusMessage string) (Action, error) {
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
		pollExternal:  pollExternal,
		sessionID:     sessionID,
		screen:        screenHome,
		isLoading:     true,
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
	return tea.Batch(m.refreshCmd(false), m.refreshTickCmd(), m.externalPollCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case refreshTickMsg:
		if m.screen != screenHome {
			return m, m.refreshTickCmd()
		}
		return m, tea.Batch(m.refreshCmd(false), m.refreshTickCmd())
	case externalPollTickMsg:
		cmds := []tea.Cmd{m.externalPollCmd()}
		if m.pollExternal != nil {
			if event := m.pollExternal(); event != nil && event.Reload != nil {
				m.statusMessage = "Account switched in another CAW window. Home reloaded to the active profile. Any active Codex session will pick up the new account after it returns here."
				cmds = append(cmds, m.refreshCmd(false))
			}
		}
		return m, tea.Batch(cmds...)
	case snapshotMsg:
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			m.isLoading = false
			return m, nil
		}
		m.errMessage = ""
		m.isLoading = false
		m.hasSnapshot = true
		m.snapshot = msg.snapshot
		m.syncSelection()
		return m, nil
	case settingsUpdatedMsg:
		if msg.err != nil {
			m.errMessage = msg.err.Error()
			return m, nil
		}
		m.errMessage = ""
		m.statusMessage = "Settings updated."
		m.snapshot.Settings.ClearTerminalBeforeLaunch = msg.value
		return m, nil
	case tea.KeyMsg:
		switch m.screen {
		case screenHome:
			return m.updateHome(msg)
		case screenAdd:
			return m.updateAdd(msg)
		case screenSettings:
			return m.updateSettings(msg)
		}
	}
	return m, nil
}

func (m *Model) syncSelection() {
	if len(m.snapshot.Profiles) == 0 {
		m.selectedIndex = 0
		m.scrollOffset = 0
		return
	}
	if m.selectedIndex >= len(m.snapshot.Profiles) || m.selectedIndex < 0 {
		m.selectedIndex = 0
	}
	for i, profile := range m.snapshot.Profiles {
		if profile.Selected {
			m.selectedIndex = i
			break
		}
	}
	m.ensureSelectionVisible(m.visibleProfileRows())
}

func (m Model) updateHome(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		m.action = Action{Type: ActionQuit}
		return m, tea.Quit
	case "r":
		m.isLoading = true
		return m, m.refreshCmd(true)
	case "a":
		m.screen = screenAdd
		m.focusIndex = 0
		m.idDirty = false
		m.nameInput.SetValue("")
		m.idInput.SetValue("")
		m.nameInput.Focus()
		m.idInput.Blur()
		return m, nil
	case "s":
		m.screen = screenSettings
		m.errMessage = ""
		return m, nil
	case "up", "k":
		if m.selectedIndex > 0 {
			m.selectedIndex--
		}
		m.ensureSelectionVisible(m.visibleProfileRows())
		return m, nil
	case "down", "j":
		if m.selectedIndex < len(m.snapshot.Profiles)-1 {
			m.selectedIndex++
		}
		m.ensureSelectionVisible(m.visibleProfileRows())
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
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := m.client.Request(ctx, "profile.select", ipc.SelectProfileRequest{
				SessionID: m.sessionID,
				ProfileID: profile.ID,
			}, nil); err != nil {
				return snapshotMsg{err: err}
			}
			return loadSnapshot(m.client, m.sessionID, true)
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
		return m, m.refreshCmd(false)
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

func (m Model) updateSettings(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		m.action = Action{Type: ActionQuit}
		return m, tea.Quit
	case "esc", "s":
		m.screen = screenHome
		m.errMessage = ""
		return m, m.refreshCmd(false)
	case "enter", " ":
		nextValue := !m.snapshot.Settings.ClearTerminalBeforeLaunch
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if err := m.client.Request(ctx, "settings.update", ipc.UpdateSettingsRequest{
				ClearTerminalBeforeLaunch: nextValue,
			}, nil); err != nil {
				return settingsUpdatedMsg{err: err}
			}
			return settingsUpdatedMsg{value: nextValue}
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "Loading Codex Auth Wrapper..."
	}
	pageStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#F4EEE1")).
		Background(lipgloss.Color("#11151C")).
		Width(m.width).
		Padding(pageVerticalPadding, pageHorizontalPadding)

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F6AE2D")).
		Render("Codex Auth Wrapper")
	subtitle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9DB4C0")).
		Render("Direct account visibility over stored Codex auth, with refreshes that do not depend on the shared app-server.")

	var body string
	switch m.screen {
	case screenAdd:
		body = m.viewAdd()
	case screenSettings:
		body = m.viewSettings()
	default:
		body = m.viewHome(m.availableBodyHeight())
	}

	return pageStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, subtitle, "", body))
}

func (m Model) viewHome(bodyHeight int) string {
	contentWidth := m.contentWidth()
	primary := "Press Enter to set up your first account"
	if len(m.snapshot.Profiles) > 0 {
		primary = "Press Enter to continue into Codex"
		if m.snapshot.Session != nil && m.snapshot.Session.ActiveThreadID != nil && *m.snapshot.Session.ActiveThreadID != "" {
			primary = "Press Enter to resume your active Codex thread"
		}
	}

	headerLines := []string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7BD389")).Render(primary),
		m.renderStatusLine(),
	}
	if sessionLine := m.renderSessionLine(); sessionLine != "" {
		headerLines = append(headerLines, lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render(sessionLine))
	}
	headerLines = append(headerLines, lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render("Keys: Enter continue  a add account  s settings  space select account  r refresh all  q quit"))

	headerPanel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A3B47")).
		Padding(1, 2).
		Width(contentWidth).
		Render(strings.Join(headerLines, "\n\n"))

	if !m.hasSnapshot && m.isLoading {
		loadingPanel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2A3B47")).
			Padding(1, 2).
			Width(contentWidth).
			Render("Loading profiles...\n\nFetching stored account metadata and live usage snapshots.")
		return lipgloss.JoinVertical(lipgloss.Left, headerPanel, "", loadingPanel)
	}

	if len(m.snapshot.Profiles) == 0 {
		emptyPanel := lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#2A3B47")).
			Padding(1, 2).
			Width(contentWidth).
			Render("No accounts linked yet.\n\nAdd a profile to import a stock Codex login, then return here to see the email, plan, linked account, and live 5-hour and weekly usage.")
		return lipgloss.JoinVertical(lipgloss.Left, headerPanel, "", emptyPanel)
	}

	listHeight := m.availableListHeight(bodyHeight, lipgloss.Height(headerPanel))
	return lipgloss.JoinVertical(lipgloss.Left, headerPanel, "", m.renderProfilesList(contentWidth, listHeight))
}

func (m Model) renderStatusLine() string {
	if m.snapshot.DegradedReason != nil {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render("Degraded: " + *m.snapshot.DegradedReason)
	}
	if m.errMessage != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render(m.errMessage)
	}
	if strings.TrimSpace(m.statusMessage) != "" {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render(m.statusMessage)
	}
	if m.isLoading && !m.hasSnapshot {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render("Loading account data...")
	}
	if m.snapshot.RefreshInProgress {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render("Showing cached account data while background refresh runs.")
	}
	clearMode := "on"
	if !m.snapshot.Settings.ClearTerminalBeforeLaunch {
		clearMode = "off"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render("Home refreshes profile data automatically, `r` forces a fresh pass, and launch screen clearing is " + clearMode + ".")
}

func (m Model) renderSessionLine() string {
	if m.snapshot.Session == nil || m.snapshot.Session.ActiveThreadID == nil || *m.snapshot.Session.ActiveThreadID == "" {
		return ""
	}
	parts := []string{"Tracked thread " + shortenID(*m.snapshot.Session.ActiveThreadID)}
	if m.snapshot.Session.ActiveThreadCwd != nil && *m.snapshot.Session.ActiveThreadCwd != "" {
		parts = append(parts, "cwd "+compactPathLabel(*m.snapshot.Session.ActiveThreadCwd))
	}
	return strings.Join(parts, "  •  ")
}

func (m *Model) renderProfilesList(width int, listHeight int) string {
	panelWidth := width
	rowHeight := 3
	visibleRows := max(1, (listHeight-2)/rowHeight)
	m.ensureSelectionVisible(visibleRows)

	start := min(m.scrollOffset, max(0, len(m.snapshot.Profiles)-visibleRows))
	end := min(len(m.snapshot.Profiles), start+visibleRows)
	rows := make([]string, 0, end-start)
	for i := start; i < end; i++ {
		rows = append(rows, m.renderProfileRow(i, panelWidth-6, rowHeight))
	}
	if len(rows) == 0 {
		rows = append(rows, "No profiles.")
	}

	footer := fmt.Sprintf("Showing %d-%d of %d", start+1, end, len(m.snapshot.Profiles))
	if end == 0 {
		footer = "Showing 0 of 0"
	}
	if len(m.snapshot.Profiles) > visibleRows {
		footer += "  •  use ↑/↓ to scroll"
	}

	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A3B47")).
		Padding(0, 2).
		Width(panelWidth).
		Height(max(5, listHeight))

	return panel.Render(strings.Join([]string{
		lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F6AE2D")).Render("Accounts"),
		strings.Join(rows, "\n"),
		lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0")).Render(footer),
	}, "\n"))
}

func (m Model) renderProfileRow(index int, width int, rowHeight int) string {
	profile := m.snapshot.Profiles[index]
	cursor := " "
	nameColor := lipgloss.Color("#F4EEE1")
	if index == m.selectedIndex {
		cursor = ">"
		nameColor = lipgloss.Color("#F6AE2D")
	}

	title := profile.Name
	if profile.Selected {
		title += " [selected]"
	}
	line1Parts := []string{
		cursor + " " + title,
		strings.ToUpper(string(profile.Health)),
	}
	if planEmail := formatPlanAndEmail(profile); planEmail != "" {
		line1Parts = append(line1Parts, planEmail)
	}
	line2 := compactUsageSegment("5h", profile.FiveHourUsagePercent, profile.FiveHourResetsAt)
	line3Parts := []string{
		compactUsageSegment("wk", profile.WeeklyUsagePercent, profile.WeeklyResetsAt),
		shortRefreshStatus(profile.LastCheckedAt, profile.LastError),
	}
	if linked := compactLinkedIdentity(profile); linked != "" {
		line3Parts = append(line3Parts, linked)
	}

	lines := []string{
		truncate(joinParts(line1Parts), width),
		truncate(line2, width),
		truncate(joinParts(line3Parts), width),
	}

	rowStyle := lipgloss.NewStyle()
	if index == m.selectedIndex {
		rowStyle = rowStyle.Foreground(nameColor)
	} else {
		rowStyle = rowStyle.Foreground(lipgloss.Color("#D9D2C5"))
	}
	return rowStyle.Render(strings.Join(lines[:rowHeight], "\n"))
}

func (m *Model) ensureSelectionVisible(visibleRows int) {
	if visibleRows <= 0 {
		visibleRows = 1
	}
	if m.selectedIndex < m.scrollOffset {
		m.scrollOffset = m.selectedIndex
	}
	if m.selectedIndex >= m.scrollOffset+visibleRows {
		m.scrollOffset = m.selectedIndex - visibleRows + 1
	}
	if m.scrollOffset < 0 {
		m.scrollOffset = 0
	}
	maxOffset := max(0, len(m.snapshot.Profiles)-visibleRows)
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}
}

func (m Model) visibleProfileRows() int {
	headerHeight := m.homeHeaderHeightEstimate()
	listHeight := m.availableListHeight(m.availableBodyHeight(), headerHeight)
	return max(1, (listHeight-2)/3)
}

func (m Model) homeHeaderHeightEstimate() int {
	height := 8
	if m.snapshot.Session != nil && m.snapshot.Session.ActiveThreadID != nil && *m.snapshot.Session.ActiveThreadID != "" {
		height += 2
	}
	return height
}

func (m Model) availableListHeight(bodyHeight int, headerHeight int) int {
	listHeight := bodyHeight - headerHeight - 1
	return max(5, listHeight)
}

func (m Model) contentWidth() int {
	return max(48, m.width-(pageHorizontalPadding*2)-4)
}

func (m Model) availableBodyHeight() int {
	titleBlockHeight := 3
	return max(8, m.height-(pageVerticalPadding*2)-titleBlockHeight)
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

func (m Model) viewSettings() string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color("#9DB4C0"))
	enabled := m.snapshot.Settings.ClearTerminalBeforeLaunch
	value := "On"
	description := "Clear the terminal before each Codex launch so return/resume cycles do not stack replay output."
	if !enabled {
		value = "Off"
		description = "Keep prior terminal scrollback visible when launching back into Codex."
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("#2A3B47")).
		Padding(1, 2).
		Width(max(64, m.width-10))

	body := []string{
		"Settings",
		"",
		fmt.Sprintf("> Clear terminal before launching Codex: %s", value),
		"",
		muted.Render(description),
		"",
		muted.Render("Enter or Space toggles this setting. Esc returns home."),
	}
	if m.errMessage != "" {
		body = append(body, "", lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B")).Render(m.errMessage))
	}
	return panel.Render(strings.Join(body, "\n"))
}

func formatPlanAndEmail(profile ipc.ProfileSummary) string {
	parts := []string{}
	if profile.Email != "" {
		parts = append(parts, profile.Email)
	}
	if profile.PlanType != "" {
		parts = append(parts, humanizePlanType(profile.PlanType))
	}
	if len(parts) == 0 {
		return "identity pending"
	}
	return strings.Join(parts, "  •  ")
}

func compactLinkedIdentity(profile ipc.ProfileSummary) string {
	parts := []string{}
	if profile.LinkedAccountID != "" {
		parts = append(parts, "ws "+shortenID(profile.LinkedAccountID))
	}
	if profile.LinkedUserID != "" {
		parts = append(parts, "user "+shortenID(profile.LinkedUserID))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "  •  ")
}

func formatUsageLine(label string, used *int, resetsAt *time.Time) string {
	if used == nil {
		return fmt.Sprintf("%s: usage unavailable", label)
	}
	parts := []string{fmt.Sprintf("%d%% used", *used)}
	if resetsAt != nil {
		parts = append(parts, "resets "+relativeReset(*resetsAt), "at "+resetsAt.Local().Format("Jan 2 3:04 PM"))
	}
	return fmt.Sprintf("%s: %s", label, strings.Join(parts, "  •  "))
}

func formatLastChecked(lastChecked *time.Time, lastError string) string {
	if lastError != "" {
		return "Refresh: " + truncate(lastError, 120)
	}
	if lastChecked == nil {
		return "Refresh: not checked yet"
	}
	return "Refresh: updated " + time.Since(*lastChecked).Round(time.Second).String() + " ago"
}

func compactUsageSegment(label string, used *int, resetsAt *time.Time) string {
	if used == nil {
		return label + " [----------] n/a"
	}
	remaining := 100 - *used
	if remaining < 0 {
		remaining = 0
	}
	segment := fmt.Sprintf("%s %s %d%% left", label, progressBar(remaining, 10), remaining)
	if resetsAt != nil {
		segment += "  " + relativeReset(*resetsAt)
	}
	return segment
}

func shortRefreshStatus(lastChecked *time.Time, lastError string) string {
	if lastError != "" {
		return "err"
	}
	if lastChecked == nil {
		return "not checked"
	}
	return "upd " + time.Since(*lastChecked).Round(time.Second).String()
}

func joinParts(parts []string) string {
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return strings.Join(out, "  •  ")
}

func relativeReset(ts time.Time) string {
	delta := time.Until(ts)
	if delta <= 0 {
		return "now"
	}
	if delta < time.Hour {
		return fmt.Sprintf("in %dm", int(delta.Minutes()+0.5))
	}
	if delta < 24*time.Hour {
		hours := int(delta.Hours())
		minutes := int((delta - time.Duration(hours)*time.Hour).Minutes())
		if minutes <= 0 {
			return fmt.Sprintf("in %dh", hours)
		}
		return fmt.Sprintf("in %dh %dm", hours, minutes)
	}
	days := int(delta.Hours()) / 24
	hours := int(delta.Hours()) % 24
	if hours == 0 {
		return fmt.Sprintf("in %dd", days)
	}
	return fmt.Sprintf("in %dd %dh", days, hours)
}

func humanizePlanType(raw string) string {
	if raw == "" {
		return ""
	}
	parts := strings.Split(strings.ReplaceAll(raw, "-", "_"), "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func shortenID(value string) string {
	if len(value) <= 18 {
		return value
	}
	return value[:8] + "..." + value[len(value)-6:]
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit-3] + "..."
}

func progressBar(percent int, width int) string {
	if width <= 0 {
		width = 1
	}
	if percent < 0 {
		percent = 0
	}
	if percent > 100 {
		percent = 100
	}
	filled := (percent*width + 50) / 100
	if filled > width {
		filled = width
	}
	return "[" + strings.Repeat("#", filled) + strings.Repeat("-", width-filled) + "]"
}

func compactPathLabel(path string) string {
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) || strings.TrimSpace(base) == "" {
		return path
	}
	return base
}

func (m Model) refreshCmd(force bool) tea.Cmd {
	return func() tea.Msg {
		return loadSnapshot(m.client, m.sessionID, force)
	}
}

func (m Model) refreshTickCmd() tea.Cmd {
	if m.snapshot.RefreshInProgress {
		return tea.Tick(backgroundRefreshTick, func(t time.Time) tea.Msg {
			return refreshTickMsg(t)
		})
	}
	interval := defaultRefreshInterval
	for _, profile := range m.snapshot.Profiles {
		if profile.WarningState != "" && profile.WarningState != "none" {
			interval = warningRefreshInterval
			break
		}
	}
	return tea.Tick(interval, func(t time.Time) tea.Msg {
		return refreshTickMsg(t)
	})
}

func loadSnapshot(client *ipc.Client, sessionID string, force bool) snapshotMsg {
	timeout := 5 * time.Second
	if force {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var snapshot ipc.HomeSnapshotResponse
	err := client.Request(ctx, "home.snapshot", ipc.HomeSnapshotRequest{
		SessionID:    sessionID,
		ForceRefresh: force,
	}, &snapshot)
	return snapshotMsg{snapshot: snapshot, err: err}
}

func (m Model) externalPollCmd() tea.Cmd {
	return tea.Tick(externalPollInterval, func(t time.Time) tea.Msg {
		return externalPollTickMsg(t)
	})
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
