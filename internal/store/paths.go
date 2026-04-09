package store

import (
	"os"
	"path/filepath"
)

const (
	WrapperDirName = ".codex-auth-wrapper"
	ProfilesDir    = "profiles"
	RuntimeDir     = "runtime"
	CodexHomeDir   = "codex-home"
	LogsDir        = "logs"
)

type Paths struct {
	Root             string
	StateFile        string
	SessionsFile     string
	BrokerFile       string
	EventsFile       string
	LogsDir          string
	ProfilesDir      string
	RuntimeDir       string
	RuntimeCodexHome string
	RuntimeAuthFile  string
	RuntimeConfigToml string
	AppServerTokenFile string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	root := filepath.Join(home, WrapperDirName)
	return NewPaths(root), nil
}

func NewPaths(root string) Paths {
	runtimeCodexHome := filepath.Join(root, RuntimeDir, CodexHomeDir)
	return Paths{
		Root:               root,
		StateFile:          filepath.Join(root, "state.json"),
		SessionsFile:       filepath.Join(root, "sessions.json"),
		BrokerFile:         filepath.Join(root, "broker.json"),
		EventsFile:         filepath.Join(root, "events.jsonl"),
		LogsDir:            filepath.Join(root, LogsDir),
		ProfilesDir:        filepath.Join(root, ProfilesDir),
		RuntimeDir:         filepath.Join(root, RuntimeDir),
		RuntimeCodexHome:   runtimeCodexHome,
		RuntimeAuthFile:    filepath.Join(runtimeCodexHome, "auth.json"),
		RuntimeConfigToml:  filepath.Join(runtimeCodexHome, "config.toml"),
		AppServerTokenFile: filepath.Join(root, RuntimeDir, "app-server-token.txt"),
	}
}

func (p Paths) ProfileDir(id string) string {
	return filepath.Join(p.ProfilesDir, id)
}

func (p Paths) ProfileFile(id string) string {
	return filepath.Join(p.ProfileDir(id), "profile.json")
}

func (p Paths) ProfileAuthFile(id string) string {
	return filepath.Join(p.ProfileDir(id), "auth.json")
}
