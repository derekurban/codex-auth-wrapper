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
	BrokerState       model.BrokerState    `json:"broker_state"`
	ActiveAuthEpochID string               `json:"active_auth_epoch_id"`
	DegradedReason    *string              `json:"degraded_reason"`
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
	SessionID    string     `json:"session_id"`
	ProfileID    string     `json:"profile_id"`
	AuthEpochID  string     `json:"auth_epoch_id"`
	GatewayURL   string     `json:"gateway_url"`
	TokenEnvName string     `json:"token_env_name"`
	Token        string     `json:"token"`
	ThreadID     *string    `json:"thread_id"`
	Mode         LaunchMode `json:"mode"`
	SelectedCwd  string     `json:"selected_cwd"`
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
}

type MessageNotice struct {
	Level   string `json:"level"`
	Message string `json:"message"`
}
