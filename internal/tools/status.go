package tools

import (
	"context"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// StatusSchema returns the JSON Schema for the sync_status tool.
func StatusSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	return s
}

// StatusHandler handles the sync_status tool.
func StatusHandler(sp PluginAccessor) ToolHandler {
	return func(_ context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		engine := sp.SyncEngine()
		if engine == nil {
			return helpers.ErrorResult("not_ready", "sync engine not initialized yet"), nil
		}

		status := engine.Status()
		m := map[string]any{
			"authenticated":    status.Authenticated,
			"sync_enabled":     status.Enabled,
			"interval_seconds": float64(status.Interval),
			"api_url":          status.APIURL,
			"device_id":        status.DeviceID,
		}

		if creds := sp.AuthManager().Creds(); creds != nil {
			m["user_email"] = creds.Email
			m["user_name"] = creds.Name
			m["team_id"] = creds.TeamID
		}

		if status.LastSync != nil {
			m["last_sync_at"] = status.LastSync.Format("2006-01-02T15:04:05Z07:00")
		}

		result, _ := structpb.NewStruct(m)
		return &pluginv1.ToolResponse{
			Success: true,
			Result:  result,
		}, nil
	}
}
