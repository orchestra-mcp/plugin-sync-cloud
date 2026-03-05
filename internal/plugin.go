// Package internal contains the sync.cloud plugin implementation.
package internal

import (
	"log"
	"os"

	"github.com/orchestra-mcp/plugin-sync-cloud/internal/auth"
	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal/tools"
	"github.com/orchestra-mcp/sdk-go/plugin"
)

// SyncPlugin is the sync.cloud plugin that pushes local data to the Orchestra
// cloud API. It provides 5 MCP tools for authentication and sync management.
//
// Engine is nil until OnBoot fires (it needs the orchestrator client). Tool
// handlers access the engine through the PluginAccessor interface so they see
// the lazily-initialized value.
type SyncPlugin struct {
	Auth    *auth.Manager
	Engine  *cloudsync.Engine
	Client  *cloudsync.CloudClient
	Storage cloudsync.StorageReader
}

// AuthManager implements tools.PluginAccessor.
func (sp *SyncPlugin) AuthManager() *auth.Manager { return sp.Auth }

// SyncEngine implements tools.PluginAccessor.
func (sp *SyncPlugin) SyncEngine() *cloudsync.Engine { return sp.Engine }

// CloudClient implements tools.PluginAccessor.
func (sp *SyncPlugin) CloudClient() *cloudsync.CloudClient { return sp.Client }

// OnBoot implements plugin.LifecycleHooks. Called after the plugin connects to
// the orchestrator with config params.
func (sp *SyncPlugin) OnBoot(config map[string]string) error {
	workspace := config["workspace"]
	if workspace == "" {
		workspace = "."
	}

	// Boot auth (load from env or disk).
	if err := sp.Auth.Boot(); err != nil {
		log.Printf("sync.cloud: auth boot warning: %v", err)
	}

	// Create sync engine now that the orchestrator client is available.
	h, _ := os.Hostname()
	if h == "" {
		h = "unknown"
	}
	deviceID := "mcp-" + h
	engine, err := cloudsync.NewEngine(sp.Client, sp.Storage, workspace, deviceID)
	if err != nil {
		return err
	}
	sp.Engine = engine

	// If already authenticated (env var or saved creds), start sync.
	if sp.Auth.IsAuthenticated() {
		creds := sp.Auth.Creds()
		engine.SetAuth(creds.Token, creds.TeamID, creds.DeviceID)
		// Start without a context — engine.Start creates its own cancellable context.
		engine.Start(nil)
		log.Printf("sync.cloud: auto-started sync for %s", creds.Email)
	}

	return nil
}

// OnShutdown implements plugin.LifecycleHooks.
func (sp *SyncPlugin) OnShutdown() error {
	if sp.Engine != nil {
		sp.Engine.Stop()
	}
	return nil
}

// RegisterTools registers all 5 MCP tools with the plugin builder.
// The tool handlers close over `sp` (as PluginAccessor) so they pick up
// sp.Engine after OnBoot sets it.
func (sp *SyncPlugin) RegisterTools(builder *plugin.PluginBuilder) {
	builder.RegisterTool(
		"orchestra_login",
		"Login to Orchestra cloud to sync local projects and features to the web dashboard. Supports email/password and 2FA.",
		tools.LoginSchema(),
		tools.LoginHandler(sp),
	)

	builder.RegisterTool(
		"orchestra_logout",
		"Logout from Orchestra cloud and stop automatic sync.",
		tools.LogoutSchema(),
		tools.LogoutHandler(sp),
	)

	builder.RegisterTool(
		"sync_status",
		"Show the current sync status: authentication, last sync time, pending changes, and configuration.",
		tools.StatusSchema(),
		tools.StatusHandler(sp),
	)

	builder.RegisterTool(
		"sync_now",
		"Trigger an immediate sync of all local changes to the Orchestra cloud.",
		tools.SyncNowSchema(),
		tools.SyncNowHandler(sp),
	)

	builder.RegisterTool(
		"sync_config",
		"Configure sync settings: enable/disable auto-sync, set polling interval, change cloud API URL.",
		tools.ConfigSchema(),
		tools.ConfigHandler(sp),
	)
}
