package tools

import (
	"context"
	"fmt"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// ConfigSchema returns the JSON Schema for the sync_config tool.
func ConfigSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"enabled": map[string]any{
				"type":        "boolean",
				"description": "Enable or disable automatic sync",
			},
			"interval_seconds": map[string]any{
				"type":        "number",
				"description": "Polling interval in seconds (minimum 5)",
			},
			"api_url": map[string]any{
				"type":        "string",
				"description": "Orchestra cloud API URL",
			},
		},
	})
	return s
}

// ConfigHandler handles the sync_config tool.
func ConfigHandler(sp PluginAccessor) ToolHandler {
	return func(_ context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		engine := sp.SyncEngine()
		if engine == nil {
			return helpers.ErrorResult("not_ready", "sync engine not initialized yet"), nil
		}

		changes := []string{}

		// Handle boolean "enabled" field.
		if v, ok := req.Arguments.Fields["enabled"]; ok {
			if bv, ok := v.Kind.(*structpb.Value_BoolValue); ok {
				engine.SetEnabled(bv.BoolValue)
				changes = append(changes, fmt.Sprintf("enabled=%v", bv.BoolValue))
			}
		}

		if interval := helpers.GetFloat64(req.Arguments, "interval_seconds"); interval >= 5 {
			engine.SetInterval(int(interval))
			changes = append(changes, fmt.Sprintf("interval=%ds", int(interval)))
		}

		if apiURL := helpers.GetString(req.Arguments, "api_url"); apiURL != "" {
			sp.CloudClient().SetBaseURL(apiURL)
			changes = append(changes, fmt.Sprintf("api_url=%s", apiURL))
		}

		status := engine.Status()
		msg := "Current sync configuration"
		if len(changes) > 0 {
			msg = fmt.Sprintf("Updated: %v", changes)
		}

		result, _ := structpb.NewStruct(map[string]any{
			"enabled":          status.Enabled,
			"interval_seconds": float64(status.Interval),
			"api_url":          status.APIURL,
			"message":          msg,
		})

		return &pluginv1.ToolResponse{
			Success: true,
			Result:  result,
		}, nil
	}
}
