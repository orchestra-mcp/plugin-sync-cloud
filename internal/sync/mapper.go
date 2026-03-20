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
	EntityType  string // "project", "feature", "note", "plan", "person", "request", "assignment_rule", "delegation", "ai_session", "session_turn", "doc", "skill", "agent", "prompt", "action"
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

	// Pattern: .skills/{id}.md — global skill
	if len(parts) == 2 && parts[0] == ".skills" && strings.HasSuffix(parts[1], ".md") {
		return &EntityMapping{
			EntityType: "skill",
			EntityID:   strings.TrimSuffix(parts[1], ".md"),
		}, nil
	}

	// Pattern: .agents/{id}.md — global agent
	if len(parts) == 2 && parts[0] == ".agents" && strings.HasSuffix(parts[1], ".md") {
		return &EntityMapping{
			EntityType: "agent",
			EntityID:   strings.TrimSuffix(parts[1], ".md"),
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
		case "prompts":
			return &EntityMapping{
				EntityType:  "prompt",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "actions":
			return &EntityMapping{
				EntityType:  "action",
				EntityID:    id,
				ProjectSlug: slug,
			}, nil
		case "delegations":
			return &EntityMapping{
				EntityType:  "delegation",
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
		case "feature", "note", "plan", "person", "request", "assignment_rule", "ai_session", "session_turn", "doc", "prompt", "action", "delegation":
			payload["body"] = string(body)
		case "skill", "agent":
			payload["content"] = string(body)
		}
	}

	// Include project_slug for project-scoped entities.
	if mapping.ProjectSlug != "" {
		switch mapping.EntityType {
		case "feature", "plan", "person", "request", "assignment_rule", "note", "doc", "prompt", "action", "delegation":
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

// EntityToPath reconstructs a local storage path from an entity type, ID, and
// optional payload (used to extract project_slug and session_id).
func EntityToPath(entityType, entityID string, payload json.RawMessage) string {
	var data map[string]interface{}
	_ = json.Unmarshal(payload, &data)

	projectSlug, _ := data["project_slug"].(string)

	switch entityType {
	case "project":
		slug := entityID
		if v, ok := data["slug"].(string); ok {
			slug = v
		}
		return slug + "/project.json"
	case "feature":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/features/" + entityID + ".md"
	case "plan":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/plans/" + entityID + ".md"
	case "person":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/persons/" + entityID + ".md"
	case "request":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/requests/" + entityID + ".md"
	case "assignment_rule":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/assignment-rules/" + entityID + ".md"
	case "note":
		if projectSlug != "" {
			return projectSlug + "/notes/" + entityID + ".md"
		}
		return ".global/notes/" + entityID + ".md"
	case "doc":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/docs/" + entityID + ".md"
	case "prompt":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/prompts/" + entityID + ".md"
	case "action":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/actions/" + entityID + ".md"
	case "delegation":
		if projectSlug == "" {
			return ""
		}
		return projectSlug + "/delegations/" + entityID + ".md"
	case "ai_session":
		return "bridge/sessions/" + entityID + ".md"
	case "session_turn":
		sessionID, _ := data["session_id"].(string)
		if sessionID == "" {
			return ""
		}
		return "bridge/sessions/" + sessionID + "/" + entityID + ".md"
	case "skill":
		return ".skills/" + entityID + ".md"
	case "agent":
		return ".agents/" + entityID + ".md"
	}
	return ""
}

// IsSyncablePath returns true if the path represents a syncable entity.
func IsSyncablePath(path string) bool {
	_, err := PathToEntity(path)
	return err == nil
}

// MembershipToPersonID generates a deterministic PERS-XXX ID from a membership UUID.
func MembershipToPersonID(membershipID string) string {
	clean := strings.ToUpper(strings.ReplaceAll(membershipID, "-", ""))
	var letters []byte
	for _, c := range clean {
		if c >= 'A' && c <= 'Z' {
			letters = append(letters, byte(c))
		}
		if len(letters) == 3 {
			break
		}
	}
	for len(letters) < 3 {
		for _, c := range clean {
			if c >= '0' && c <= '9' {
				letters = append(letters, byte('A'+c-'0'))
			}
			if len(letters) == 3 {
				break
			}
		}
		if len(letters) < 3 {
			letters = append(letters, 'X')
		}
	}
	return "PERS-" + string(letters)
}

// MapMembershipRole converts cloud membership roles to person roles.
func MapMembershipRole(memberRole string) string {
	switch memberRole {
	case "owner", "admin":
		return "lead"
	case "viewer":
		return "qa"
	default:
		return "developer"
	}
}
