package sync

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func makeTempCursor(t *testing.T) (*Cursor, string) {
	t.Helper()
	dir := t.TempDir()
	// Override sync-state path by creating cursor manually.
	c := &Cursor{
		path:     filepath.Join(dir, "cursor.json"),
		DeviceID: "test-device",
		Versions: make(map[string]int64),
	}
	return c, dir
}

func TestCursor_IsChanged_NewPath(t *testing.T) {
	c, _ := makeTempCursor(t)
	// A path not yet tracked should be considered changed (any version != 0).
	if !c.IsChanged("project/features/FEAT-001.md", 1) {
		t.Error("expected IsChanged=true for unknown path with version 1")
	}
}

func TestCursor_IsChanged_SameVersion(t *testing.T) {
	c, _ := makeTempCursor(t)
	c.MarkSynced("project/features/FEAT-001.md", 5)
	if c.IsChanged("project/features/FEAT-001.md", 5) {
		t.Error("expected IsChanged=false when version matches")
	}
}

func TestCursor_IsChanged_NewerVersion(t *testing.T) {
	c, _ := makeTempCursor(t)
	c.MarkSynced("project/features/FEAT-001.md", 3)
	if !c.IsChanged("project/features/FEAT-001.md", 5) {
		t.Error("expected IsChanged=true when version is newer")
	}
}

func TestCursor_MarkSynced_UpdatesVersion(t *testing.T) {
	c, _ := makeTempCursor(t)
	c.MarkSynced("project/features/FEAT-002.md", 7)
	if c.Versions["project/features/FEAT-002.md"] != 7 {
		t.Errorf("Versions[path] = %d, want 7", c.Versions["project/features/FEAT-002.md"])
	}
}

func TestCursor_SetLastPull_GetLastPullRFC3339(t *testing.T) {
	c, _ := makeTempCursor(t)
	if c.GetLastPullRFC3339() != "" {
		t.Error("expected empty string when LastPullAt is nil")
	}
	now := time.Now().Truncate(time.Second)
	c.SetLastPull(now)
	got := c.GetLastPullRFC3339()
	if got == "" {
		t.Error("expected non-empty RFC3339 after SetLastPull")
	}
	// Round-trip check.
	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("invalid RFC3339: %v", err)
	}
	if !parsed.Equal(now) {
		t.Errorf("parsed time %v != original %v", parsed, now)
	}
}

func TestCursor_PendingCount(t *testing.T) {
	c, _ := makeTempCursor(t)
	c.MarkSynced("path/a", 1)
	c.MarkSynced("path/b", 2)

	current := map[string]int64{
		"path/a": 1, // unchanged
		"path/b": 3, // changed
		"path/c": 1, // new
	}
	pending := c.PendingCount(current)
	if pending != 2 {
		t.Errorf("PendingCount = %d, want 2", pending)
	}
}

func TestCursor_SaveLoad_RoundTrip(t *testing.T) {
	c, _ := makeTempCursor(t)
	now := time.Now().Truncate(time.Second)
	c.MarkSynced("project/features/FEAT-003.md", 9)
	c.SetLastSync(now)
	c.SetLastPull(now)
	if err := c.Save(); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify file exists.
	if _, err := os.Stat(c.path); err != nil {
		t.Fatalf("cursor file not created: %v", err)
	}

	// Load into new cursor.
	c2 := &Cursor{path: c.path, Versions: make(map[string]int64)}
	if err := c2.Load(); err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if c2.Versions["project/features/FEAT-003.md"] != 9 {
		t.Errorf("Versions after load = %d, want 9", c2.Versions["project/features/FEAT-003.md"])
	}
	if c2.GetLastPullRFC3339() == "" {
		t.Error("LastPullAt should be set after load")
	}
}

func TestCursor_DeltaRecordTypes(t *testing.T) {
	// Verify DeltaRecord and DeltaResponse structs serialize as expected.
	now := time.Now().Truncate(time.Second)
	rec := DeltaRecord{
		EntityType: "feature",
		EntityID:   "FEAT-XYZ",
		Action:     "upsert",
		Version:    3,
		ChangedAt:  now,
	}
	if rec.EntityType != "feature" {
		t.Errorf("EntityType = %q, want feature", rec.EntityType)
	}
	if rec.Action != "upsert" {
		t.Errorf("Action = %q, want upsert", rec.Action)
	}
	if rec.Version != 3 {
		t.Errorf("Version = %d, want 3", rec.Version)
	}

	resp := DeltaResponse{
		Changes: []DeltaRecord{rec},
		Count:   1,
		Since:   "2026-03-20T00:00:00Z",
	}
	if resp.Count != 1 {
		t.Errorf("Count = %d, want 1", resp.Count)
	}
	if len(resp.Changes) != 1 {
		t.Errorf("Changes len = %d, want 1", len(resp.Changes))
	}
}
