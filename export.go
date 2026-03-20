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

// SetOnPullFunc sets a callback for when a pull completes with doc-affecting entity types.
type SetOnPullFunc func(cb func(entityTypes []string))

// SetOnImportFunc sets a callback for when a full import completes after login.
type SetOnImportFunc func(cb func(importedCount int))

// AuthInfo holds the current authentication state for the tunnel auto-connect flow.
type AuthInfo struct {
	Authenticated bool
	Token         string
	TeamID        string
}

// AuthCheckFunc returns the current authentication state.
type AuthCheckFunc func() AuthInfo

// Register adds all 5 sync-cloud tools to the builder and returns:
//   - cleanup: must be called on shutdown to stop the sync engine
//   - syncNow: triggers an immediate sync (returns nil if engine not ready)
//   - setAuth: updates auth credentials (e.g., from tunnel claim)
//   - setOnPull: sets a callback for post-pull doc regeneration
//   - setOnImport: sets a callback for post-import doc regeneration + RAG rebuild
//   - authCheck: returns current auth state for tunnel auto-connect
func Register(builder *plugin.PluginBuilder, sender Sender) (Cleanup, SyncNowFunc, SetAuthFunc, SetOnPullFunc, SetOnImportFunc, AuthCheckFunc) {
	apiURL := os.Getenv("ORCHESTRA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	cloudClient := cloudsync.NewCloudClient(apiURL)

	authStore, err := auth.NewStore()
	if err != nil {
		log.Printf("sync.cloud: init auth store: %v", err)
		return func() {}, nil, nil, nil, nil, func() AuthInfo { return AuthInfo{} }
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

	setOnPull := func(cb func(entityTypes []string)) {
		engine := sp.Engine
		if engine == nil {
			return
		}
		engine.OnPullComplete = cb
	}

	setOnImport := func(cb func(importedCount int)) {
		engine := sp.Engine
		if engine == nil {
			return
		}
		engine.OnImportComplete = cb
	}

	authCheck := func() AuthInfo {
		return AuthInfo{
			Authenticated: authMgr.IsAuthenticated(),
			Token:         authMgr.Token(),
			TeamID:        func() string {
				if c := authMgr.Creds(); c != nil {
					return c.TeamID
				}
				return ""
			}(),
		}
	}

	return cleanup, syncNow, setAuth, setOnPull, setOnImport, authCheck
}

// senderAdapter wraps a Sender to satisfy cloudsync.StorageReader.
type senderAdapter struct {
	sender Sender
}

func (a *senderAdapter) Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error) {
	return a.sender.Send(ctx, req)
}
