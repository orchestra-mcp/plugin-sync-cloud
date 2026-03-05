package sync

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"
)

// EntityMapping holds the parsed entity type and ID from a storage path.
type EntityMapping struct {
	EntityType  string // "project", "feature", "note"
	EntityID    string
	ProjectSlug string
}

// PathToEntity maps a local storage path to a cloud entity type and ID.
// Examples:
//
//	"my-project/project.json"          -> project, "my-project"
//	"my-project/features/FEAT-ABC.md"  -> feature, "FEAT-ABC"
//	"notes/note-123.md"                -> note,    "note-123"
func PathToEntity(path string) (*EntityMapping, error) {
	parts := strings.Split(filepath.ToSlash(path), "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("path too short: %s", path)
	}

	// Pattern: {slug}/project.json
	if len(parts) == 2 && parts[1] == "project.json" {
		return &EntityMapping{
			EntityType:  "project",
			EntityID:    parts[0],
			ProjectSlug: parts[0],
		}, nil
	}

	// Pattern: {slug}/features/{id}.md
	if len(parts) == 3 && parts[1] == "features" && strings.HasSuffix(parts[2], ".md") {
		featureID := strings.TrimSuffix(parts[2], ".md")
		return &EntityMapping{
			EntityType:  "feature",
			EntityID:    featureID,
			ProjectSlug: parts[0],
		}, nil
	}

	// Pattern: notes/{id}.md (or any top-level dir)
	if len(parts) == 2 && parts[0] == "notes" && strings.HasSuffix(parts[1], ".md") {
		noteID := strings.TrimSuffix(parts[1], ".md")
		return &EntityMapping{
			EntityType: "note",
			EntityID:   noteID,
		}, nil
	}

	return nil, fmt.Errorf("unrecognized path pattern: %s", path)
}

// MetadataToSyncRecord converts storage metadata + body to a SyncRecord.
func MetadataToSyncRecord(mapping *EntityMapping, metadata *structpb.Struct, body []byte, version int64, teamID string) (*SyncRecord, error) {
	// Build the payload from metadata fields.
	payload := make(map[string]interface{})
	if metadata != nil {
		payload = metadata.AsMap()
	}

	// Include body content for features/notes.
	if len(body) > 0 && (mapping.EntityType == "feature" || mapping.EntityType == "note") {
		payload["body"] = string(body)
	}

	// Ensure project_slug is in feature payloads.
	if mapping.EntityType == "feature" && mapping.ProjectSlug != "" {
		payload["project_slug"] = mapping.ProjectSlug
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	idempotencyKey := fmt.Sprintf("%s:%s:%d", mapping.EntityType, mapping.EntityID, version)

	record := &SyncRecord{
		EntityType:     mapping.EntityType,
		EntityID:       mapping.EntityID,
		Action:         "upsert",
		Payload:        payloadJSON,
		Version:        version,
		IdempotencyKey: idempotencyKey,
	}
	if teamID != "" {
		record.TeamID = teamID
	}
	return record, nil
}

// IsSyncablePath returns true if the path represents a syncable entity.
func IsSyncablePath(path string) bool {
	_, err := PathToEntity(path)
	return err == nil
}
