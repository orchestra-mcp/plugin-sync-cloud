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

// Register adds all 5 sync-cloud tools to the builder and returns a cleanup
// function that must be called on shutdown to stop the sync engine.
func Register(builder *plugin.PluginBuilder, sender Sender) Cleanup {
	apiURL := os.Getenv("ORCHESTRA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	cloudClient := cloudsync.NewCloudClient(apiURL)

	authStore, err := auth.NewStore()
	if err != nil {
		log.Printf("sync.cloud: init auth store: %v", err)
		return func() {}
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

	return func() {
		_ = sp.OnShutdown()
	}
}

// senderAdapter wraps a Sender to satisfy cloudsync.StorageReader.
type senderAdapter struct {
	sender Sender
}

func (a *senderAdapter) Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error) {
	return a.sender.Send(ctx, req)
}
