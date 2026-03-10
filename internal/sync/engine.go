package sync

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
)

// StorageReader is the interface for reading local storage via the orchestrator.
type StorageReader interface {
	Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error)
}

// SyncStatus holds the current engine state.
type SyncStatus struct {
	Authenticated bool       `json:"authenticated"`
	Enabled       bool       `json:"enabled"`
	LastSync      *time.Time `json:"last_sync_at,omitempty"`
	PendingCount  int        `json:"pending_count"`
	Interval      int        `json:"interval_seconds"`
	APIURL        string     `json:"api_url"`
	UserEmail     string     `json:"user_email,omitempty"`
	DeviceID      string     `json:"device_id,omitempty"`
}

// Engine polls local storage for changes and pushes them to the cloud.
type Engine struct {
	client   *CloudClient
	cursor   *Cursor
	storage  StorageReader
	interval time.Duration
	enabled  bool
	mu       sync.Mutex
	cancel   context.CancelFunc

	// Auth state (set externally).
	token    string
	teamID   string
	tunnelID string
	deviceID string
}

// NewEngine creates a new sync engine.
func NewEngine(client *CloudClient, storage StorageReader, workspace, deviceID string) (*Engine, error) {
	cursor, err := NewCursor(workspace, deviceID)
	if err != nil {
		return nil, err
	}
	return &Engine{
		client:   client,
		cursor:   cursor,
		storage:  storage,
		interval: 30 * time.Second,
		enabled:  true,
		deviceID: deviceID,
	}, nil
}

// SetAuth updates the authentication credentials.
func (e *Engine) SetAuth(token, teamID, tunnelID, deviceID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.token = token
	e.teamID = teamID
	e.tunnelID = tunnelID
	e.deviceID = deviceID
}

// ClearAuth removes credentials and stops sync.
func (e *Engine) ClearAuth() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.token = ""
	e.teamID = ""
}

// SetInterval updates the polling interval.
func (e *Engine) SetInterval(seconds int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if seconds < 5 {
		seconds = 5
	}
	e.interval = time.Duration(seconds) * time.Second
}

// SetEnabled enables or disables the sync engine.
func (e *Engine) SetEnabled(enabled bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.enabled = enabled
}

// Start begins the background polling loop. If ctx is nil, context.Background()
// is used; the loop is stopped via Stop() or when the context is cancelled.
func (e *Engine) Start(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	e.mu.Lock()
	if e.cancel != nil {
		e.mu.Unlock()
		return // Already running.
	}
	ctx, e.cancel = context.WithCancel(ctx)
	e.mu.Unlock()

	go func() {
		ticker := time.NewTicker(e.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.mu.Lock()
				enabled := e.enabled
				hasAuth := e.token != ""
				e.mu.Unlock()
				if enabled && hasAuth {
					if err := e.syncOnce(ctx); err != nil {
						log.Printf("sync.cloud: sync error: %v", err)
					}
				}
			}
		}
	}()
}

// Stop halts the background polling.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		e.cancel()
		e.cancel = nil
	}
}

// SyncNow triggers an immediate sync and returns the results.
func (e *Engine) SyncNow(ctx context.Context) (applied, skipped, errors int, err error) {
	return e.doSync(ctx)
}

// Status returns the current engine state.
func (e *Engine) Status() *SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return &SyncStatus{
		Authenticated: e.token != "",
		Enabled:       e.enabled,
		LastSync:      e.cursor.LastSync,
		Interval:      int(e.interval.Seconds()),
		APIURL:        e.client.BaseURL(),
		DeviceID:      e.deviceID,
	}
}

func (e *Engine) syncOnce(ctx context.Context) error {
	_, _, _, err := e.doSync(ctx)
	return err
}

func (e *Engine) doSync(ctx context.Context) (applied, skipped, errors int, err error) {
	e.mu.Lock()
	token := e.token
	teamID := e.teamID
	tunnelID := e.tunnelID
	deviceID := e.deviceID
	e.mu.Unlock()

	if token == "" {
		return 0, 0, 0, fmt.Errorf("not authenticated")
	}

	// List all files in storage.
	entries, err := e.listStorage(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("list storage: %w", err)
	}

	// Find changed files.
	var records []SyncRecord
	for _, entry := range entries {
		if !IsSyncablePath(entry.Path) {
			continue
		}
		if !e.cursor.IsChanged(entry.Path, entry.Version) {
			continue
		}

		mapping, err := PathToEntity(entry.Path)
		if err != nil {
			continue
		}

		// Read the full file content.
		resp, err := e.readStorage(ctx, entry.Path)
		if err != nil {
			log.Printf("sync.cloud: read %s: %v", entry.Path, err)
			continue
		}

		record, err := MetadataToSyncRecord(mapping, resp.Metadata, resp.Content, entry.Version, teamID)
		if err != nil {
			log.Printf("sync.cloud: map %s: %v", entry.Path, err)
			continue
		}
		records = append(records, *record)
	}

	if len(records) == 0 {
		return 0, 0, 0, nil
	}

	// Push in batches of 50.
	batchSize := 50
	for i := 0; i < len(records); i += batchSize {
		end := i + batchSize
		if end > len(records) {
			end = len(records)
		}
		batch := records[i:end]

		pushResp, err := e.client.Push(token, PushRequest{
			DeviceID: deviceID,
			TunnelID: tunnelID,
			Records:  batch,
		})
		if err != nil {
			return applied, skipped, errors + len(batch), fmt.Errorf("push batch: %w", err)
		}

		// Update cursor for successful pushes.
		for j, result := range pushResp.Results {
			switch result.Status {
			case "applied":
				applied++
			case "skipped":
				skipped++
			default:
				errors++
				if result.Error != "" {
					log.Printf("sync.cloud: error for %s: %s", result.EntityID, result.Error)
				}
				continue
			}
			// Find the original record to get path + version.
			rec := batch[j]
			// Reconstruct path from entity type + ID.
			path := e.entityToPath(rec.EntityType, rec.EntityID)
			if path != "" {
				e.cursor.MarkSynced(path, rec.Version)
			}
		}
	}

	now := time.Now()
	e.cursor.SetLastSync(now)
	_ = e.cursor.Save()

	return applied, skipped, errors, nil
}

func (e *Engine) entityToPath(entityType, entityID string) string {
	// Reverse lookup: find the path in cursor that matches this entity.
	// For now, scan entries to find a match.
	// This is a simplification — in production we'd maintain a reverse index.
	e.cursor.mu.Lock()
	defer e.cursor.mu.Unlock()
	for path := range e.cursor.Versions {
		mapping, err := PathToEntity(path)
		if err != nil {
			continue
		}
		if mapping.EntityType == entityType && mapping.EntityID == entityID {
			return path
		}
	}
	return ""
}

// listStorage queries the orchestrator for all storage entries.
func (e *Engine) listStorage(ctx context.Context) ([]storageEntry, error) {
	var allEntries []storageEntry

	// List .md files (features, notes).
	mdEntries, err := e.listStoragePattern(ctx, "", "*.md")
	if err != nil {
		return nil, err
	}
	allEntries = append(allEntries, mdEntries...)

	// List .json files (projects, config).
	jsonEntries, err := e.listStoragePattern(ctx, "", "*.json")
	if err != nil {
		return nil, err
	}
	allEntries = append(allEntries, jsonEntries...)

	return allEntries, nil
}

type storageEntry struct {
	Path    string
	Version int64
}

func (e *Engine) listStoragePattern(ctx context.Context, prefix, pattern string) ([]storageEntry, error) {
	resp, err := e.storage.Send(ctx, &pluginv1.PluginRequest{
		RequestId: helpers.NewUUID(),
		Request: &pluginv1.PluginRequest_StorageList{
			StorageList: &pluginv1.StorageListRequest{
				Prefix:      prefix,
				Pattern:     pattern,
				StorageType: "markdown",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	sl := resp.GetStorageList()
	if sl == nil {
		return nil, fmt.Errorf("unexpected response for storage list")
	}

	var entries []storageEntry
	for _, e := range sl.Entries {
		entries = append(entries, storageEntry{Path: e.Path, Version: e.Version})
	}
	return entries, nil
}

func (e *Engine) readStorage(ctx context.Context, path string) (*pluginv1.StorageReadResponse, error) {
	resp, err := e.storage.Send(ctx, &pluginv1.PluginRequest{
		RequestId: helpers.NewUUID(),
		Request: &pluginv1.PluginRequest_StorageRead{
			StorageRead: &pluginv1.StorageReadRequest{
				Path:        path,
				StorageType: "markdown",
			},
		},
	})
	if err != nil {
		return nil, err
	}
	sr := resp.GetStorageRead()
	if sr == nil {
		return nil, fmt.Errorf("unexpected response for storage read")
	}
	return sr, nil
}
