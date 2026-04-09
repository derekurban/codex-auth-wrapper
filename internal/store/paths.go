package store

import (
	"os"
	"path/filepath"
)

const (
	WrapperDirName      = ".codex-auth-wrapper"
	DefaultCodexDirName = ".codex"
	ProfilesDir         = "profiles"
	RuntimeDir          = "runtime"
	LogsDir             = "logs"
)

type Paths struct {
	Root               string
	StateFile          string
	SessionsFile       string
	BrokerFile         string
	EventsFile         string
	LogsDir            string
	ProfilesDir        string
	RuntimeDir         string
	CodexHome          string
	CodexAuthFile      string
	CodexConfigToml    string
	AppServerTokenFile string
}

func DefaultPaths() (Paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	root := filepath.Join(home, WrapperDirName)
	return NewPaths(root, filepath.Join(home, DefaultCodexDirName)), nil
}

func NewPaths(root string, codexHome string) Paths {
	return Paths{
		Root:               root,
		StateFile:          filepath.Join(root, "state.json"),
		SessionsFile:       filepath.Join(root, "sessions.json"),
		BrokerFile:         filepath.Join(root, "broker.json"),
		EventsFile:         filepath.Join(root, "events.jsonl"),
		LogsDir:            filepath.Join(root, LogsDir),
		ProfilesDir:        filepath.Join(root, ProfilesDir),
		RuntimeDir:         filepath.Join(root, RuntimeDir),
		CodexHome:          codexHome,
		CodexAuthFile:      filepath.Join(codexHome, "auth.json"),
		CodexConfigToml:    filepath.Join(codexHome, "config.toml"),
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
