package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

const FileName = "obs_viewer_settings.json"

// DefaultLastDirectory is the directory shown in the file browser and used
// for preload when the user has no saved state yet. Empty means "no
// opinion" — /api/browse falls back to the user's home directory, which
// is the only portable default across Windows, Linux and macOS. Deployments
// with a fixed drop location can set last_directory in the settings file.
const DefaultLastDirectory = ""

// Settings holds all persisted user preferences. Modules that need to
// persist their own state should add a field here with an `omitempty`
// JSON tag so the settings file stays readable when the module is absent.
type Settings struct {
	LastDirectory    string `json:"last_directory"`
	AutoLoadPrevious bool   `json:"auto_load_previous"`
}

// Store manages loading and saving settings from a JSON file
// next to the executable.
type Store struct {
	mu       sync.RWMutex
	path     string
	settings Settings
}

// NewStore creates a Store. The settings file lives in baseDir.
func NewStore(baseDir string) *Store {
	return &Store{
		path: filepath.Join(baseDir, FileName),
	}
}

// Load reads settings from disk. Missing file is not an error.
func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			// First run — populate defaults and persist so operators can
			// discover all configurable fields in the JSON file.
			s.settings.LastDirectory = DefaultLastDirectory
			if out, merr := json.MarshalIndent(s.settings, "", "  "); merr == nil {
				_ = os.WriteFile(s.path, out, 0644)
			}
			return nil
		}
		return err
	}
	if err := json.Unmarshal(data, &s.settings); err != nil {
		return err
	}
	if s.settings.LastDirectory == "" {
		s.settings.LastDirectory = DefaultLastDirectory
	}
	return nil
}

// Save writes current settings to disk.
func (s *Store) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s.settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0644)
}

// Get returns a copy of the current settings.
func (s *Store) Get() Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

// Update applies a mutation function and saves to disk.
func (s *Store) Update(fn func(*Settings)) error {
	s.mu.Lock()
	fn(&s.settings)
	s.mu.Unlock()
	return s.Save()
}

// Path returns the settings file path.
func (s *Store) Path() string {
	return s.path
}
