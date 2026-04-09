package model

import "time"

const SchemaVersion = 1

type HomeFocus string

const (
	HomeFocusPrimaryAction HomeFocus = "primary_action"
	HomeFocusAccountsList  HomeFocus = "accounts_list"
)

type SessionState string

const (
	SessionStateHome           SessionState = "home"
	SessionStateLaunchingCodex SessionState = "launching_codex"
	SessionStateInCodex        SessionState = "in_codex"
	SessionStateReturningHome  SessionState = "returning_home"
	SessionStateReloading      SessionState = "reloading"
	SessionStateResumeFailed   SessionState = "resume_failed"
	SessionStateClosed         SessionState = "closed"
)

type BrokerState string

const (
	BrokerStateStarting         BrokerState = "starting"
	BrokerStateHomeReady        BrokerState = "home_ready"
	BrokerStateLaunchingCodex   BrokerState = "launching_codex"
	BrokerStateActive           BrokerState = "active"
	BrokerStateSwitchingProfile BrokerState = "switching_profile"
	BrokerStateReloading        BrokerState = "reloading_sessions"
	BrokerStateDegraded         BrokerState = "degraded"
	BrokerStateStopped          BrokerState = "stopped"
)

type ServerState string

const (
	ServerStateStarting ServerState = "starting"
	ServerStateHealthy  ServerState = "healthy"
	ServerStateStopping ServerState = "stopping"
	ServerStateFailed   ServerState = "failed"
	ServerStateStopped  ServerState = "stopped"
)

type ProfileHealth string

const (
	ProfileHealthUnknown    ProfileHealth = "unknown"
	ProfileHealthHealthy    ProfileHealth = "healthy"
	ProfileHealthWarning    ProfileHealth = "warning"
	ProfileHealthExhausted  ProfileHealth = "exhausted"
	ProfileHealthAuthFailed ProfileHealth = "auth_failed"
	ProfileHealthDisabled   ProfileHealth = "disabled"
)

type ProfileWarningState string

const (
	ProfileWarningNone     ProfileWarningState = "none"
	ProfileWarningFiveHour ProfileWarningState = "five_hour_near_limit"
	ProfileWarningWeekly   ProfileWarningState = "weekly_near_limit"
	ProfileWarningBoth     ProfileWarningState = "both_near_limit"
)

type StateFile struct {
	SchemaVersion        int             `json:"schema_version"`
	SelectedProfileID    *string         `json:"selected_profile_id"`
	ProfileOrder         []string        `json:"profile_order"`
	CurrentAuthEpochID   string          `json:"current_auth_epoch_id"`
	NextAuthEpochCounter int             `json:"next_auth_epoch_counter"`
	HomeScreen           HomeScreenState `json:"home_screen"`
	Settings             WrapperSettings `json:"settings"`
	CreatedAt            time.Time       `json:"created_at"`
	UpdatedAt            time.Time       `json:"updated_at"`
}

type HomeScreenState struct {
	LastFocus              HomeFocus `json:"last_focus"`
	LastSelectedAccountRow *string   `json:"last_selected_account_row"`
}

type WrapperSettings struct {
	ClearTerminalBeforeLaunch *bool `json:"clear_terminal_before_launch,omitempty"`
}

func (s WrapperSettings) ClearTerminalEnabled() bool {
	if s.ClearTerminalBeforeLaunch == nil {
		return true
	}
	return *s.ClearTerminalBeforeLaunch
}

type SessionsFile struct {
	SchemaVersion int                      `json:"schema_version"`
	Sessions      map[string]SessionRecord `json:"sessions"`
	UpdatedAt     time.Time                `json:"updated_at"`
}

type SessionRecord struct {
	SessionID           string       `json:"session_id"`
	State               SessionState `json:"state"`
	Cwd                 string       `json:"cwd"`
	ActiveThreadID      *string      `json:"active_thread_id"`
	ActiveThreadCwd     *string      `json:"active_thread_cwd"`
	LastKnownProfileID  *string      `json:"last_known_profile_id"`
	LastSeenAuthEpochID *string      `json:"last_seen_auth_epoch_id"`
	ResumePending       bool         `json:"resume_pending"`
	ResumeAllowed       bool         `json:"resume_allowed"`
	CodexChildPID       *int         `json:"codex_child_pid"`
	LastEnteredCodexAt  *time.Time   `json:"last_entered_codex_at"`
	LastReturnedHomeAt  *time.Time   `json:"last_returned_home_at"`
	CreatedAt           time.Time    `json:"created_at"`
	UpdatedAt           time.Time    `json:"updated_at"`
}

type BrokerFile struct {
	SchemaVersion     int           `json:"schema_version"`
	BrokerState       BrokerState   `json:"broker_state"`
	ActiveAuthEpochID string        `json:"active_auth_epoch_id"`
	ActiveProfileID   *string       `json:"active_profile_id"`
	Server            ServerInfo    `json:"server"`
	SwitchContext     SwitchContext `json:"switch_context"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

type ServerInfo struct {
	State             ServerState `json:"state"`
	ListenURL         *string     `json:"listen_url"`
	AuthMode          *string     `json:"auth_mode"`
	StartedAt         *time.Time  `json:"started_at"`
	LastRestartReason *string     `json:"last_restart_reason"`
}

type SwitchContext struct {
	InProgress           bool       `json:"in_progress"`
	FromProfileID        *string    `json:"from_profile_id"`
	ToProfileID          *string    `json:"to_profile_id"`
	InitiatedBySessionID *string    `json:"initiated_by_session_id"`
	InitiatedAt          *time.Time `json:"initiated_at"`
}

type ProfileFile struct {
	SchemaVersion     int           `json:"schema_version"`
	ID                string        `json:"id"`
	Name              string        `json:"name"`
	Enabled           bool          `json:"enabled"`
	AuthFile          string        `json:"auth_file"`
	SelectionPriority int           `json:"selection_priority"`
	Status            ProfileStatus `json:"status"`
	LastSelectedAt    *time.Time    `json:"last_selected_at"`
	CreatedAt         time.Time     `json:"created_at"`
	UpdatedAt         time.Time     `json:"updated_at"`
}

type ProfileStatus struct {
	Health               ProfileHealth       `json:"health"`
	Email                string              `json:"email"`
	PlanType             string              `json:"plan_type"`
	LinkedAccountID      string              `json:"linked_account_id"`
	LinkedUserID         string              `json:"linked_user_id"`
	FiveHourUsagePercent *int                `json:"five_hour_usage_percent"`
	WeeklyUsagePercent   *int                `json:"weekly_usage_percent"`
	FiveHourWindowLabel  string              `json:"five_hour_window_label"`
	WeeklyWindowLabel    string              `json:"weekly_window_label"`
	FiveHourResetsAt     *time.Time          `json:"five_hour_resets_at"`
	WeeklyResetsAt       *time.Time          `json:"weekly_resets_at"`
	LastCheckedAt        *time.Time          `json:"last_checked_at"`
	WarningState         ProfileWarningState `json:"warning_state"`
	LastError            string              `json:"last_error"`
}

func NewInitialState(now time.Time) StateFile {
	return StateFile{
		SchemaVersion:        SchemaVersion,
		ProfileOrder:         []string{},
		CurrentAuthEpochID:   "epoch-0000000",
		NextAuthEpochCounter: 1,
		HomeScreen: HomeScreenState{
			LastFocus: HomeFocusPrimaryAction,
		},
		Settings: WrapperSettings{
			ClearTerminalBeforeLaunch: boolPtr(true),
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
}

func NewInitialSessions(now time.Time) SessionsFile {
	return SessionsFile{
		SchemaVersion: SchemaVersion,
		Sessions:      map[string]SessionRecord{},
		UpdatedAt:     now,
	}
}

func NewInitialBroker(now time.Time) BrokerFile {
	return BrokerFile{
		SchemaVersion:     SchemaVersion,
		BrokerState:       BrokerStateStarting,
		ActiveAuthEpochID: "epoch-0000000",
		Server: ServerInfo{
			State: ServerStateStopped,
		},
		SwitchContext: SwitchContext{},
		UpdatedAt:     now,
	}
}

func boolPtr(v bool) *bool {
	return &v
}
