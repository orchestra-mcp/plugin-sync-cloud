package tools

import (
	"context"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// LogoutSchema returns the JSON Schema for the orchestra_logout tool.
func LogoutSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	return s
}

// LogoutHandler handles the orchestra_logout tool.
func LogoutHandler(sp PluginAccessor) ToolHandler {
	return func(_ context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		if engine := sp.SyncEngine(); engine != nil {
			engine.Stop()
			engine.ClearAuth()
		}

		if err := sp.AuthManager().Logout(); err != nil {
			return helpers.ErrorResult("auth_error", "logout failed: "+err.Error()), nil
		}

		result, _ := structpb.NewStruct(map[string]any{
			"status":  "logged_out",
			"message": "Logged out. Sync has been stopped.",
		})

		return &pluginv1.ToolResponse{
			Success: true,
			Result:  result,
		}, nil
	}
}
