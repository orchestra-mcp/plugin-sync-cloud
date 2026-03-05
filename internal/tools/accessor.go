package tools

import (
	"context"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal/auth"
	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
)

// ToolHandler is the standard tool handler function signature.
type ToolHandler = func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error)

// PluginAccessor provides lazy access to the sync plugin's components.
// The Engine may be nil before OnBoot fires; tool handlers must nil-check it.
type PluginAccessor interface {
	AuthManager() *auth.Manager
	SyncEngine() *cloudsync.Engine
	CloudClient() *cloudsync.CloudClient
}
