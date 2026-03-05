// Package auth manages Orchestra cloud authentication credentials.
// Credentials are stored at ~/.orchestra/auth.json with 0600 permissions.
package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Credentials holds the authenticated user's cloud session.
type Credentials struct {
	Token    string `json:"token"`
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	TeamID   string `json:"team_id,omitempty"`
	DeviceID string `json:"device_id"`
	APIURL   string `json:"api_url"`
}

// Store reads and writes auth credentials to ~/.orchestra/auth.json.
type Store struct {
	mu   sync.Mutex
	path string
}

// NewStore creates a new auth store. The file is created lazily on first Save.
func NewStore() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".orchestra")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create dir: %w", err)
	}
	return &Store{path: filepath.Join(dir, "auth.json")}, nil
}

// Load reads credentials from disk. Returns nil if no credentials exist.
func (s *Store) Load() (*Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read auth file: %w", err)
	}
	var creds Credentials
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("parse auth file: %w", err)
	}
	return &creds, nil
}

// Save writes credentials to disk with 0600 permissions.
func (s *Store) Save(creds *Credentials) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	if err := os.WriteFile(s.path, data, 0600); err != nil {
		return fmt.Errorf("write auth file: %w", err)
	}
	return nil
}

// Clear removes the credentials file.
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove auth file: %w", err)
	}
	return nil
}
