package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/derekurban/codex-auth-wrapper/internal/model"
)

type Store struct {
	Paths Paths
}

func New(paths Paths) *Store {
	return &Store{Paths: paths}
}

func (s *Store) EnsureLayout(now time.Time) error {
	dirs := []string{
		s.Paths.Root,
		s.Paths.LogsDir,
		s.Paths.ProfilesDir,
		s.Paths.RuntimeDir,
		s.Paths.RuntimeCodexHome,
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.Paths.StateFile); errors.Is(err, os.ErrNotExist) {
		if err := writeJSONAtomic(s.Paths.StateFile, model.NewInitialState(now)); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.Paths.SessionsFile); errors.Is(err, os.ErrNotExist) {
		if err := writeJSONAtomic(s.Paths.SessionsFile, model.NewInitialSessions(now)); err != nil {
			return err
		}
	}
	if _, err := os.Stat(s.Paths.BrokerFile); errors.Is(err, os.ErrNotExist) {
		if err := writeJSONAtomic(s.Paths.BrokerFile, model.NewInitialBroker(now)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) LoadState() (model.StateFile, error) {
	var out model.StateFile
	err := readJSONFile(s.Paths.StateFile, &out)
	return out, err
}

func (s *Store) SaveState(in model.StateFile) error {
	return writeJSONAtomic(s.Paths.StateFile, in)
}

func (s *Store) LoadSessions() (model.SessionsFile, error) {
	var out model.SessionsFile
	err := readJSONFile(s.Paths.SessionsFile, &out)
	return out, err
}

func (s *Store) SaveSessions(in model.SessionsFile) error {
	return writeJSONAtomic(s.Paths.SessionsFile, in)
}

func (s *Store) LoadBroker() (model.BrokerFile, error) {
	var out model.BrokerFile
	err := readJSONFile(s.Paths.BrokerFile, &out)
	return out, err
}

func (s *Store) SaveBroker(in model.BrokerFile) error {
	return writeJSONAtomic(s.Paths.BrokerFile, in)
}

func (s *Store) LoadProfile(id string) (model.ProfileFile, error) {
	var out model.ProfileFile
	err := readJSONFile(s.Paths.ProfileFile(id), &out)
	return out, err
}

func (s *Store) SaveProfile(in model.ProfileFile) error {
	if err := os.MkdirAll(s.Paths.ProfileDir(in.ID), 0o755); err != nil {
		return err
	}
	return writeJSONAtomic(s.Paths.ProfileFile(in.ID), in)
}

func (s *Store) ProfileExists(id string) bool {
	_, err := os.Stat(s.Paths.ProfileFile(id))
	return err == nil
}

func (s *Store) ListProfiles() ([]model.ProfileFile, error) {
	entries, err := os.ReadDir(s.Paths.ProfilesDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	out := make([]model.ProfileFile, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		profile, err := s.LoadProfile(entry.Name())
		if err != nil {
			return nil, err
		}
		out = append(out, profile)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *Store) CopyProfileAuthToRuntime(profileID string) error {
	src := s.Paths.ProfileAuthFile(profileID)
	dst := s.Paths.RuntimeAuthFile
	return copyFile(src, dst)
}

func (s *Store) CopyRuntimeAuthToProfile(profileID string) error {
	src := s.Paths.RuntimeAuthFile
	if _, err := os.Stat(src); err != nil {
		return err
	}
	dst := s.Paths.ProfileAuthFile(profileID)
	if err := os.MkdirAll(s.Paths.ProfileDir(profileID), 0o755); err != nil {
		return err
	}
	return copyFile(src, dst)
}

func (s *Store) SaveProfileAuthFrom(profileID, authPath string) error {
	if err := os.MkdirAll(s.Paths.ProfileDir(profileID), 0o755); err != nil {
		return err
	}
	return copyFile(authPath, s.Paths.ProfileAuthFile(profileID))
}

func readJSONFile(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func writeJSONAtomic(path string, in any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(in, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}
