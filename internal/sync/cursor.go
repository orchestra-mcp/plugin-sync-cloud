package sync

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cursor tracks which file versions have been synced for a workspace.
// State is persisted at ~/.orchestra/sync-state/{workspace-hash}.json.
type Cursor struct {
	mu       sync.Mutex
	path     string
	DeviceID string            `json:"device_id"`
	Versions map[string]int64  `json:"versions"` // storage path -> last synced version
	LastSync *time.Time        `json:"last_sync_at,omitempty"`
}

// NewCursor creates a cursor for the given workspace directory.
func NewCursor(workspace, deviceID string) (*Cursor, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("get home dir: %w", err)
	}
	dir := filepath.Join(home, ".orchestra", "sync-state")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("create sync-state dir: %w", err)
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(workspace)))[:12]
	path := filepath.Join(dir, hash+".json")

	c := &Cursor{
		path:     path,
		DeviceID: deviceID,
		Versions: make(map[string]int64),
	}
	_ = c.Load()
	return c, nil
}

// Load reads the cursor state from disk.
func (c *Cursor) Load() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, c)
}

// Save writes the cursor state to disk.
func (c *Cursor) Save() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0644)
}

// IsChanged returns true if the path's version differs from the last sync.
func (c *Cursor) IsChanged(path string, version int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.Versions[path] != version
}

// MarkSynced updates the stored version for a path.
func (c *Cursor) MarkSynced(path string, version int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Versions[path] = version
}

// SetLastSync records the sync timestamp.
func (c *Cursor) SetLastSync(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.LastSync = &t
}

// PendingCount returns the number of paths that differ from synced versions.
func (c *Cursor) PendingCount(current map[string]int64) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	count := 0
	for path, version := range current {
		if c.Versions[path] != version {
			count++
		}
	}
	return count
}
