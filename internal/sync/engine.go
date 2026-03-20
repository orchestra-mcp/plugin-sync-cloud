package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/globaldb"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
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
	client    *CloudClient
	cursor    *Cursor
	storage   StorageReader
	workspace string
	interval  time.Duration
	enabled   bool
	mu        sync.Mutex
	cancel    context.CancelFunc

	// Auth state (set externally).
	token    string
	teamID   string
	tunnelID string
	deviceID string

	// OnPullComplete is called after a pull applies changes that affect
	// workspace docs (skills, agents). The caller (CLI) sets this to
	// trigger GenerateWorkspaceDocs.
	OnPullComplete func(entityTypes []string)

	// OnImportComplete is called after FullImport finishes. The caller
	// (CLI) uses this to regenerate workspace docs and rebuild RAG indexes.
	OnImportComplete func(importedCount int)
}

// NewEngine creates a new sync engine.
func NewEngine(client *CloudClient, storage StorageReader, workspace, deviceID string) (*Engine, error) {
	cursor, err := NewCursor(workspace, deviceID)
	if err != nil {
		return nil, err
	}
	return &Engine{
		client:    client,
		cursor:    cursor,
		storage:   storage,
		workspace: workspace,
		interval:  30 * time.Second,
		enabled:   true,
		deviceID:  deviceID,
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
	// Phase 1: Push local changes to cloud.
	pushApplied, pushSkipped, pushErrors, pushErr := e.doPush(ctx)
	applied += pushApplied
	skipped += pushSkipped
	errors += pushErrors

	// Phase 1b: Sync workspaces (separate from entity sync).
	if syncErr := e.syncWorkspaces(ctx); syncErr != nil {
		log.Printf("sync.cloud: workspace sync: %v", syncErr)
	}

	// Phase 2: Pull remote changes from cloud.
	pullApplied, pullSkipped, pullErrors, pullErr := e.doPull(ctx)
	applied += pullApplied
	skipped += pullSkipped
	errors += pullErrors

	// Return the first error encountered.
	if pushErr != nil {
		return applied, skipped, errors, pushErr
	}
	return applied, skipped, errors, pullErr
}

// syncWorkspaces pushes local workspaces to the cloud via POST /api/workspaces/sync.
// Workspaces live in globaldb (not project storage), so they need separate sync.
func (e *Engine) syncWorkspaces(_ context.Context) error {
	e.mu.Lock()
	token := e.token
	e.mu.Unlock()

	if token == "" {
		return nil
	}

	// Read workspaces directly from globaldb (same machine).
	workspaces, err := globaldb.ListWorkspaces()
	if err != nil {
		return fmt.Errorf("list workspaces: %w", err)
	}

	if len(workspaces) == 0 {
		return nil
	}

	for _, ws := range workspaces {
		if err := e.client.SyncWorkspace(token, WorkspaceSyncRequest{
			Name:          ws.Name,
			Folders:       ws.Folders,
			PrimaryFolder: ws.PrimaryFolder,
			Source:        "desktop",
			LocalID:       ws.ID,
		}); err != nil {
			log.Printf("sync.cloud: sync workspace %s: %v", ws.ID, err)
		}
	}

	return nil
}

func (e *Engine) doPush(ctx context.Context) (applied, skipped, errors int, err error) {
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

// doPull fetches changes from the cloud and applies them locally via storage write.
func (e *Engine) doPull(ctx context.Context) (applied, skipped, errors int, err error) {
	e.mu.Lock()
	token := e.token
	deviceID := e.deviceID
	e.mu.Unlock()

	if token == "" {
		return 0, 0, 0, fmt.Errorf("not authenticated")
	}

	since := e.cursor.GetLastPullRFC3339()

	resp, err := e.client.Pull(token, PullRequest{
		DeviceID: deviceID,
		Since:    since,
		Limit:    500,
	})
	if err != nil {
		return 0, 0, 0, fmt.Errorf("pull: %w", err)
	}

	if len(resp.Records) == 0 {
		return 0, 0, 0, nil
	}

	var latestTime time.Time
	docEntityTypes := map[string]bool{"skill": true, "agent": true}
	pulledEntityTypesSet := map[string]bool{}
	for _, rec := range resp.Records {
		path := EntityToPath(rec.EntityType, rec.EntityID, rec.Payload)
		if path == "" {
			log.Printf("sync.cloud: pull: cannot map %s/%s to path", rec.EntityType, rec.EntityID)
			skipped++
			continue
		}

		// Check local version — skip if local is newer (LWW).
		if !e.cursor.IsChanged(path, rec.Version) {
			skipped++
			continue
		}

		if rec.Action == "delete" {
			if err := e.deleteStorage(ctx, path); err != nil {
				log.Printf("sync.cloud: pull delete %s: %v", path, err)
				errors++
				continue
			}
		} else {
			// Extract metadata and body from payload.
			metadata, body := splitPayload(rec.EntityType, rec.Payload)
			if err := e.writeStorage(ctx, path, body, metadata, rec.Version); err != nil {
				log.Printf("sync.cloud: pull write %s: %v", path, err)
				errors++
				continue
			}
			// Write back to .claude/ filesystem so Claude Code sees the update.
			if e.workspace != "" {
				e.writeClaudeFile(rec.EntityType, rec.EntityID, body)
			}
		}

		e.cursor.MarkSynced(path, rec.Version)
		applied++

		if docEntityTypes[rec.EntityType] {
			pulledEntityTypesSet[rec.EntityType] = true
		}

		if rec.CreatedAt.After(latestTime) {
			latestTime = rec.CreatedAt
		}
	}

	if !latestTime.IsZero() {
		e.cursor.SetLastPull(latestTime)
	}
	_ = e.cursor.Save()

	// Notify the caller if doc-affecting entities (skills, agents) were pulled.
	if e.OnPullComplete != nil && len(pulledEntityTypesSet) > 0 {
		var pulledEntityTypes []string
		for t := range pulledEntityTypesSet {
			pulledEntityTypes = append(pulledEntityTypes, t)
		}
		e.OnPullComplete(pulledEntityTypes)
	}

	return applied, skipped, errors, nil
}

// FullImport downloads all data from the cloud and writes it to local storage.
// Called after initial login to bootstrap local state.
func (e *Engine) FullImport(ctx context.Context) (int, error) {
	e.mu.Lock()
	token := e.token
	e.mu.Unlock()
	if token == "" {
		return 0, fmt.Errorf("not authenticated")
	}

	resp, err := e.client.Export(token)
	if err != nil {
		return 0, fmt.Errorf("export: %w", err)
	}

	imported := 0

	// Import each entity type.
	type entityBatch struct {
		entityType string
		items      []json.RawMessage
	}
	batches := []entityBatch{
		{"project", resp.Projects},
		{"feature", resp.Features},
		{"note", resp.Notes},
		{"plan", resp.Plans},
		{"person", resp.Persons},
		{"doc", resp.Docs},
		{"prompt", resp.Prompts},
		{"action", resp.Actions},
		{"skill", resp.Skills},
		{"agent", resp.Agents},
	}

	for _, batch := range batches {
		for _, raw := range batch.items {
			var data map[string]interface{}
			if err := json.Unmarshal(raw, &data); err != nil {
				log.Printf("sync.cloud: import unmarshal %s: %v", batch.entityType, err)
				continue
			}

			// Extract entity ID.
			entityID := extractEntityID(batch.entityType, data)
			if entityID == "" {
				continue
			}

			// Resolve path.
			path := EntityToPath(batch.entityType, entityID, raw)
			if path == "" {
				continue
			}

			// Extract version.
			var version int64 = 1
			if v, ok := data["version"].(float64); ok {
				version = int64(v)
			}

			// Split payload into metadata + body.
			metadata, body := splitPayload(batch.entityType, raw)

			if err := e.writeStorage(ctx, path, body, metadata, version); err != nil {
				log.Printf("sync.cloud: import write %s/%s: %v", batch.entityType, entityID, err)
				continue
			}

			// Write back to .claude/ filesystem for skills and agents.
			if e.workspace != "" {
				e.writeClaudeFile(batch.entityType, entityID, body)
			}

			e.cursor.MarkSynced(path, version)
			imported++
		}
	}

	// Import team members as persons.
	// Find the default project slug from the imported projects.
	var defaultProjectSlug string
	for _, raw := range resp.Projects {
		var proj map[string]interface{}
		if json.Unmarshal(raw, &proj) == nil {
			if slug, ok := proj["slug"].(string); ok {
				defaultProjectSlug = slug
				break
			}
		}
	}

	if defaultProjectSlug != "" && len(resp.Members) > 0 {
		for _, raw := range resp.Members {
			var member MemberRow
			if err := json.Unmarshal(raw, &member); err != nil {
				log.Printf("sync.cloud: import member unmarshal: %v", err)
				continue
			}
			if member.MembershipID == "" {
				continue
			}

			personID := MembershipToPersonID(member.MembershipID)
			role := MapMembershipRole(member.Role)

			personData := map[string]interface{}{
				"id":           personID,
				"project_slug": defaultProjectSlug,
				"name":         member.Name,
				"email":        member.Email,
				"role":         role,
				"status":       member.Status,
				"integrations": map[string]string{
					"cloud_membership_id": member.MembershipID,
					"avatar_url":          member.AvatarURL,
				},
			}
			payload, err := json.Marshal(personData)
			if err != nil {
				continue
			}
			metadata, body := splitPayload("person", payload)
			path := defaultProjectSlug + "/persons/" + personID + ".md"

			if err := e.writeStorage(ctx, path, body, metadata, 1); err != nil {
				log.Printf("sync.cloud: import member %s: %v", personID, err)
				continue
			}
			e.cursor.MarkSynced(path, 1)
			imported++
		}
	}

	now := time.Now()
	e.cursor.SetLastSync(now)
	e.cursor.SetLastPull(now)
	_ = e.cursor.Save()

	// Notify caller to regenerate docs and rebuild RAG indexes.
	if e.OnImportComplete != nil && imported > 0 {
		e.OnImportComplete(imported)
	}

	return imported, nil
}

// extractEntityID pulls the entity ID from a data map based on entity type.
func extractEntityID(entityType string, data map[string]interface{}) string {
	switch entityType {
	case "project":
		if v, ok := data["slug"].(string); ok {
			return v
		}
	case "feature":
		if v, ok := data["feature_id"].(string); ok {
			return v
		}
		if v, ok := data["id"].(string); ok {
			return v
		}
	case "plan":
		if v, ok := data["plan_id"].(string); ok {
			return v
		}
		if v, ok := data["id"].(string); ok {
			return v
		}
	case "person":
		if v, ok := data["person_id"].(string); ok {
			return v
		}
		if v, ok := data["id"].(string); ok {
			return v
		}
	case "doc":
		if v, ok := data["doc_id"].(string); ok {
			return v
		}
		if v, ok := data["slug"].(string); ok {
			return v
		}
	case "note", "prompt", "action":
		if v, ok := data["id"].(string); ok {
			return v
		}
	case "skill", "agent":
		if v, ok := data["slug"].(string); ok {
			return v
		}
	}
	// Fallback to "id".
	if v, ok := data["id"].(string); ok {
		return v
	}
	return ""
}

// splitPayload separates the body/content/script field from metadata fields.
func splitPayload(entityType string, payload json.RawMessage) (*structpb.Struct, []byte) {
	var data map[string]interface{}
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil, nil
	}

	var body []byte
	switch entityType {
	case "feature", "note", "plan", "person", "request", "assignment_rule", "ai_session", "session_turn", "doc", "prompt", "action", "delegation":
		if v, ok := data["body"].(string); ok {
			body = []byte(v)
			delete(data, "body")
		}
	case "skill", "agent":
		if v, ok := data["content"].(string); ok {
			body = []byte(v)
			delete(data, "content")
		}
	}

	metadata, err := structpb.NewStruct(data)
	if err != nil {
		return nil, body
	}
	return metadata, body
}

// writeStorage writes a file via the storage plugin.
func (e *Engine) writeStorage(ctx context.Context, path string, content []byte, metadata *structpb.Struct, version int64) error {
	_, err := e.storage.Send(ctx, &pluginv1.PluginRequest{
		RequestId: helpers.NewUUID(),
		Request: &pluginv1.PluginRequest_StorageWrite{
			StorageWrite: &pluginv1.StorageWriteRequest{
				Path:            path,
				Content:         content,
				Metadata:        metadata,
				ExpectedVersion: -1, // Upsert unconditionally (cloud is authoritative on pull)
				StorageType:     "markdown",
			},
		},
	})
	return err
}

// deleteStorage deletes a file via the storage plugin.
func (e *Engine) deleteStorage(ctx context.Context, path string) error {
	_, err := e.storage.Send(ctx, &pluginv1.PluginRequest{
		RequestId: helpers.NewUUID(),
		Request: &pluginv1.PluginRequest_StorageDelete{
			StorageDelete: &pluginv1.StorageDeleteRequest{
				Path:        path,
				StorageType: "markdown",
			},
		},
	})
	return err
}

// writeClaudeFile writes pulled skill/agent content back to .claude/ filesystem
// so Claude Code slash commands and agents stay in sync with cloud changes.
func (e *Engine) writeClaudeFile(entityType, entityID string, content []byte) {
	if len(content) == 0 || e.workspace == "" {
		return
	}
	// entityID for skills/agents is the slug (e.g. "qa-testing", "frontend-dev").
	slug := strings.TrimSuffix(entityID, ".md")
	if slug == "" {
		return
	}
	var filePath string
	switch entityType {
	case "skill":
		filePath = filepath.Join(e.workspace, ".claude", "skills", slug, "SKILL.md")
	case "agent":
		filePath = filepath.Join(e.workspace, ".claude", "agents", slug+".md")
	default:
		return
	}
	if err := os.MkdirAll(filepath.Dir(filePath), 0755); err != nil {
		log.Printf("sync.cloud: writeClaudeFile mkdir %s: %v", filePath, err)
		return
	}
	if err := os.WriteFile(filePath, content, 0644); err != nil {
		log.Printf("sync.cloud: writeClaudeFile write %s: %v", filePath, err)
	}
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
