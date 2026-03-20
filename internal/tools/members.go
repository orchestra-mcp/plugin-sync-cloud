package tools

import (
	"context"
	"fmt"
	"strings"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// MembersSchema returns the JSON Schema for the list_team_members tool.
func MembersSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	return s
}

// MembersHandler handles the list_team_members tool.
func MembersHandler(sp PluginAccessor) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		authMgr := sp.AuthManager()
		if !authMgr.IsAuthenticated() {
			return helpers.ErrorResult("not_authenticated", "Not logged in. Call orchestra_login first."), nil
		}

		cc := sp.CloudClient()
		token := authMgr.Token()

		resp, err := cc.TeamMembers(token)
		if err != nil {
			return helpers.ErrorResult("api_error", fmt.Sprintf("Failed to fetch team members: %v", err)), nil
		}

		if len(resp.Members) == 0 {
			return helpers.TextResult("No team members found."), nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("## Team Members (%d)\n\n", len(resp.Members)))
		sb.WriteString("| Person ID | Name | Email | Role | Status |\n")
		sb.WriteString("|-----------|------|-------|------|--------|\n")
		for _, m := range resp.Members {
			personID := cloudsync.MembershipToPersonID(m.MembershipID)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s |\n",
				personID, m.Name, m.Email, m.Role, m.Status))
		}

		return helpers.TextResult(sb.String()), nil
	}
}
