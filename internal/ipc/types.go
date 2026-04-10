package ipc

import (
	"encoding/json"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

const PipeName = `\\.\pipe\caw-broker`

type Envelope struct {
	Type      string          `json:"type"`
	RequestID string          `json:"request_id,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     string          `json:"error,omitempty"`
}

const (
	TypeRequest  = "request"
	TypeResponse = "response"
	TypeEvent    = "event"
)

type Request struct {
	Method string `json:"method"`
}

type Event struct {
	Name string `json:"name"`
}

type RegisterSessionRequest struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

type Empty struct{}

type HomeSnapshotRequest struct {
	SessionID    string `json:"session_id"`
	ForceRefresh bool   `json:"force_refresh"`
}

type HomeSnapshotResponse struct {
	SelectedProfileID *string              `json:"selected_profile_id"`
	Profiles          []ProfileSummary     `json:"profiles"`
	Session           *model.SessionRecord `json:"session"`
	Settings          WrapperSettings      `json:"settings"`
	BrokerState       model.BrokerState    `json:"broker_state"`
	ActiveAuthEpochID string               `json:"active_auth_epoch_id"`
	PendingSwitch     *PendingSwitch       `json:"pending_switch"`
	RefreshInProgress bool                 `json:"refresh_in_progress"`
	DegradedReason    *string              `json:"degraded_reason"`
}

type WrapperSettings struct {
	ClearTerminalBeforeLaunch bool `json:"clear_terminal_before_launch"`
}

type ProfileSummary struct {
	ID                   string                    `json:"id"`
	Name                 string                    `json:"name"`
	Enabled              bool                      `json:"enabled"`
	Health               model.ProfileHealth       `json:"health"`
	WarningState         model.ProfileWarningState `json:"warning_state"`
	Email                string                    `json:"email"`
	PlanType             string                    `json:"plan_type"`
	LinkedAccountID      string                    `json:"linked_account_id"`
	LinkedUserID         string                    `json:"linked_user_id"`
	FiveHourUsagePercent *int                      `json:"five_hour_usage_percent"`
	WeeklyUsagePercent   *int                      `json:"weekly_usage_percent"`
	FiveHourResetsAt     *time.Time                `json:"five_hour_resets_at"`
	WeeklyResetsAt       *time.Time                `json:"weekly_resets_at"`
	LastCheckedAt        *time.Time                `json:"last_checked_at"`
	LastError            string                    `json:"last_error"`
	Selected             bool                      `json:"selected"`
	PendingTarget        bool                      `json:"pending_target"`
}

type PendingSwitch struct {
	FromProfileID             *string    `json:"from_profile_id"`
	ToProfileID               *string    `json:"to_profile_id"`
	ToProfileName             string     `json:"to_profile_name"`
	InitiatedByCurrentSession bool       `json:"initiated_by_current_session"`
	InitiatedAt               *time.Time `json:"initiated_at"`
	BlockingBusySessionCount  int        `json:"blocking_busy_session_count"`
	LiveCodexSessionCount     int        `json:"live_codex_session_count"`
	CanForce                  bool       `json:"can_force"`
	CanCancel                 bool       `json:"can_cancel"`
}

type AddProfileRequest struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	AuthPath string `json:"auth_path"`
}

type SelectProfileRequest struct {
	SessionID string `json:"session_id"`
	ProfileID string `json:"profile_id"`
}

type ProfileSelectOutcome string

const (
	ProfileSelectOutcomeNoop           ProfileSelectOutcome = "noop"
	ProfileSelectOutcomeSwitched       ProfileSelectOutcome = "switched"
	ProfileSelectOutcomePending        ProfileSelectOutcome = "pending"
	ProfileSelectOutcomeUpdatedPending ProfileSelectOutcome = "updated_pending"
)

type SelectProfileResponse struct {
	Outcome         ProfileSelectOutcome `json:"outcome"`
	ActiveProfileID *string              `json:"active_profile_id"`
	PendingSwitch   *PendingSwitch       `json:"pending_switch,omitempty"`
}

type ForcePendingSwitchRequest struct {
	SessionID string `json:"session_id"`
}

type CancelPendingSwitchRequest struct {
	SessionID string `json:"session_id"`
}

type PendingSwitchResponse struct {
	Cancelled     bool           `json:"cancelled,omitempty"`
	Committed     bool           `json:"committed,omitempty"`
	PendingSwitch *PendingSwitch `json:"pending_switch,omitempty"`
}

type PrepareLaunchRequest struct {
	SessionID string `json:"session_id"`
	Cwd       string `json:"cwd"`
}

type LaunchMode string

const (
	LaunchModeFresh  LaunchMode = "fresh"
	LaunchModeResume LaunchMode = "resume"
)

type LaunchSpec struct {
	SessionID    string          `json:"session_id"`
	ProfileID    string          `json:"profile_id"`
	AuthEpochID  string          `json:"auth_epoch_id"`
	GatewayURL   string          `json:"gateway_url"`
	TokenEnvName string          `json:"token_env_name"`
	Token        string          `json:"token"`
	ThreadID     *string         `json:"thread_id"`
	Mode         LaunchMode      `json:"mode"`
	SelectedCwd  string          `json:"selected_cwd"`
	Settings     WrapperSettings `json:"settings"`
}

type UpdateSettingsRequest struct {
	ClearTerminalBeforeLaunch bool `json:"clear_terminal_before_launch"`
}

type ReturnHomeRequest struct {
	SessionID string `json:"session_id"`
}

type UnregisterSessionRequest struct {
	SessionID string `json:"session_id"`
}

type UpdateSessionStateRequest struct {
	SessionID     string             `json:"session_id"`
	State         model.SessionState `json:"state"`
	CodexChildPID *int               `json:"codex_child_pid,omitempty"`
}

type StatusSnapshot struct {
	BrokerState       model.BrokerState `json:"broker_state"`
	ActiveProfileID   *string           `json:"active_profile_id"`
	ActiveAuthEpochID string            `json:"active_auth_epoch_id"`
	SessionCount      int               `json:"session_count"`
	ServerState       model.ServerState `json:"server_state"`
	ServerURL         *string           `json:"server_url"`
	UpdatedAt         time.Time         `json:"updated_at"`
}

type ReloadNotice struct {
	AuthEpochID string  `json:"auth_epoch_id"`
	ProfileID   *string `json:"profile_id"`
	Reason      string  `json:"reason"`
	Forced      bool    `json:"forced"`
	Message     string  `json:"message,omitempty"`
}

type SwitchNotice struct {
	Phase         string         `json:"phase"`
	Message       string         `json:"message"`
	PendingSwitch *PendingSwitch `json:"pending_switch,omitempty"`
}

type MessageNotice struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}
