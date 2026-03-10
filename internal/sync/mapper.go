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
	EntityType  string // "project", "feature", "note", "plan", "person", "request", "assignment_rule", "ai_session", "session_turn", "doc"
	EntityID    string
	ProjectSlug string
	ParentID    string // For session turns: the session ID
}

// PathToEntity maps a local storage path to a cloud entity type and ID.
// Examples:
//
//	"my-project/project.json"                      -> project,         "my-project"
//	"my-project/features/FEAT-ABC.md"              -> feature,         "FEAT-ABC"
//	"my-project/plans/PLAN-ABC.md"                 -> plan,            "PLAN-ABC"
//	"my-project/persons/PERS-ABC.md"               -> person,          "PERS-ABC"
//	"my-project/requests/REQ-ABC.md"               -> request,         "REQ-ABC"
//	"my-project/assignment-rules/RULE-ABC.md"      -> assignment_rule, "RULE-ABC"
//	"my-project/docs/my-doc-slug.md"               -> doc,             "my-doc-slug"
//	"my-project/notes/NOTE-ABC.md"                 -> note,            "NOTE-ABC" (project-scoped)
//	".global/notes/NOTE-ABC.md"                    -> note,            "NOTE-ABC" (global)
//	"bridge/sessions/{uuid}.md"                    -> ai_session,      "{uuid}"
//	"bridge/sessions/{uuid}/turn-001.md"           -> session_turn,    "turn-001"
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

	// Pattern: bridge/sessions/{id}.md — AI session
	if len(parts) == 3 && parts[0] == "bridge" && parts[1] == "sessions" && strings.HasSuffix(parts[2], ".md") {
		sessionID := strings.TrimSuffix(parts[2], ".md")
		return &EntityMapping{
			EntityType: "ai_session",
			EntityID:   sessionID,
		}, nil
	}

	// Pattern: bridge/sessions/{id}/turn-NNN.md — session turn
	if len(parts) == 4 && parts[0] == "bridge" && parts[1] == "sessions" && strings.HasSuffix(parts[3], ".md") {
		sessionID := parts[2]
		turnID := strings.TrimSuffix(parts[3], ".md")
		return &EntityMapping{
			EntityType: "session_turn",
			EntityID:   turnID,
			ParentID:   sessionID,
		}, nil
	}

	// Three-part patterns: {slug}/{dir}/{id}.md
	if len(parts) == 3 && strings.HasSuffix(parts[2], ".md") {
		id := strings.TrimSuffix(parts[2], ".md")
		slug := parts[0]

		switch parts[1] {
		case "features":
			return &EntityMapping{
				EntityType:  "feature",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "plans":
			return &EntityMapping{
				EntityType:  "plan",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "persons":
			return &EntityMapping{
				EntityType:  "person",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "requests":
			return &EntityMapping{
				EntityType:  "request",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "assignment-rules":
			return &EntityMapping{
				EntityType:  "assignment_rule",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "notes":
			return &EntityMapping{
				EntityType:  "note",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "docs":
			return &EntityMapping{
				EntityType:  "doc",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		}
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

	// Include body content for all markdown entities.
	if len(body) > 0 {
		switch mapping.EntityType {
		case "feature", "note", "plan", "person", "request", "assignment_rule", "ai_session", "session_turn", "doc":
			payload["body"] = string(body)
		}
	}

	// Include project_slug for project-scoped entities.
	if mapping.ProjectSlug != "" {
		switch mapping.EntityType {
		case "feature", "plan", "person", "request", "assignment_rule", "note", "doc":
			payload["project_slug"] = mapping.ProjectSlug
		}
	}

	// Include parent session ID for turns.
	if mapping.EntityType == "session_turn" && mapping.ParentID != "" {
		payload["session_id"] = mapping.ParentID
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
