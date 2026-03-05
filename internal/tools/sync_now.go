package tools

import (
	"context"
	"fmt"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// SyncNowSchema returns the JSON Schema for the sync_now tool.
func SyncNowSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	})
	return s
}

// SyncNowHandler handles the sync_now tool.
func SyncNowHandler(sp PluginAccessor) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		engine := sp.SyncEngine()
		if engine == nil {
			return helpers.ErrorResult("not_ready", "sync engine not initialized yet"), nil
		}

		applied, skipped, errors, err := engine.SyncNow(ctx)
		if err != nil {
			return helpers.ErrorResult("sync_error", "sync failed: "+err.Error()), nil
		}

		msg := fmt.Sprintf("Sync complete: %d applied, %d skipped, %d errors", applied, skipped, errors)
		result, _ := structpb.NewStruct(map[string]any{
			"applied": float64(applied),
			"skipped": float64(skipped),
			"errors":  float64(errors),
			"message": msg,
		})

		return &pluginv1.ToolResponse{
			Success: true,
			Result:  result,
		}, nil
	}
}
