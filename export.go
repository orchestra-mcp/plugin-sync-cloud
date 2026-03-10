package synccloud

import (
	"context"
	"log"
	"os"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal/auth"
	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
	"github.com/orchestra-mcp/sdk-go/plugin"
)

// Sender is the interface that the in-process router satisfies.
type Sender interface {
	Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error)
}

// Cleanup is a function to call on shutdown to stop the sync engine.
type Cleanup func()

// SyncNowFunc triggers an immediate sync and returns results.
type SyncNowFunc func(ctx context.Context) (applied, skipped, errors int, err error)

// SetAuthFunc updates the sync engine's authentication credentials.
type SetAuthFunc func(token, teamID, tunnelID, deviceID string)

// Register adds all 5 sync-cloud tools to the builder and returns:
//   - cleanup: must be called on shutdown to stop the sync engine
//   - syncNow: triggers an immediate sync (returns nil if engine not ready)
//   - setAuth: updates auth credentials (e.g., from tunnel claim)
func Register(builder *plugin.PluginBuilder, sender Sender) (Cleanup, SyncNowFunc, SetAuthFunc) {
	apiURL := os.Getenv("ORCHESTRA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	cloudClient := cloudsync.NewCloudClient(apiURL)

	authStore, err := auth.NewStore()
	if err != nil {
		log.Printf("sync.cloud: init auth store: %v", err)
		return func() {}, nil, nil
	}
	authMgr := auth.NewManager(authStore, cloudClient)

	sp := &internal.SyncPlugin{
		Auth:    authMgr,
		Client:  cloudClient,
		Storage: &senderAdapter{sender: sender},
	}

	sp.RegisterTools(builder)

	// Boot the plugin immediately (in-process — no QUIC connect phase).
	if err := sp.OnBoot(map[string]string{}); err != nil {
		log.Printf("sync.cloud: boot error: %v", err)
	}

	cleanup := func() {
		_ = sp.OnShutdown()
	}

	syncNow := func(ctx context.Context) (int, int, int, error) {
		engine := sp.Engine
		if engine == nil {
			return 0, 0, 0, nil
		}
		return engine.SyncNow(ctx)
	}

	setAuth := func(token, teamID, tunnelID, deviceID string) {
		engine := sp.Engine
		if engine == nil {
			return
		}
		engine.SetAuth(token, teamID, tunnelID, deviceID)
		// Start the engine if not already running.
		engine.Start(nil)
	}

	return cleanup, syncNow, setAuth
}

// senderAdapter wraps a Sender to satisfy cloudsync.StorageReader.
type senderAdapter struct {
	sender Sender
}

func (a *senderAdapter) Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error) {
	return a.sender.Send(ctx, req)
}
