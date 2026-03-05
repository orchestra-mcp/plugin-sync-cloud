// Command sync-cloud is the entry point for the sync.cloud plugin binary.
// It authenticates with the Orchestra cloud API and pushes local project/feature
// data to the web dashboard, enabling team-scoped project visibility.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal"
	"github.com/orchestra-mcp/plugin-sync-cloud/internal/auth"
	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
	"github.com/orchestra-mcp/sdk-go/plugin"
)

func main() {
	// Initialize cloud client.
	apiURL := os.Getenv("ORCHESTRA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	cloudClient := cloudsync.NewCloudClient(apiURL)

	// Initialize auth store.
	authStore, err := auth.NewStore()
	if err != nil {
		log.Fatalf("sync.cloud: init auth store: %v", err)
	}
	authMgr := auth.NewManager(authStore, cloudClient)

	// Create a storage adapter that forwards to the orchestrator client.
	// The plugin's OrchestratorClient() is nil until Run connects, so the
	// adapter defers the lookup to call time.
	adapter := &clientAdapter{}

	// SyncPlugin implements LifecycleHooks (OnBoot/OnShutdown) so the engine
	// is created lazily after the orchestrator connection is established.
	sp := &internal.SyncPlugin{
		Auth:    authMgr,
		Client:  cloudClient,
		Storage: adapter,
	}

	builder := plugin.New("sync.cloud").
		Version("0.1.0").
		Description("Cloud sync — pushes local projects and features to the Orchestra web dashboard").
		Author("Orchestra").
		Binary("sync-cloud").
		NeedsStorage("markdown").
		Lifecycle(sp)

	sp.RegisterTools(builder)

	p := builder.BuildWithTools()
	p.ParseFlags()

	// Wire the adapter to the plugin so it can resolve the orchestrator client.
	adapter.plugin = p

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := p.Run(ctx); err != nil {
		log.Fatalf("sync.cloud: %v", err)
	}
}

// clientAdapter implements cloudsync.StorageReader by forwarding requests to the
// plugin's orchestrator client. This allows the sync engine to read storage
// through the QUIC connection established during Run.
type clientAdapter struct {
	plugin *plugin.Plugin
}

func (a *clientAdapter) Send(ctx context.Context, req *pluginv1.PluginRequest) (*pluginv1.PluginResponse, error) {
	client := a.plugin.OrchestratorClient()
	if client == nil {
		return nil, fmt.Errorf("orchestrator client not connected")
	}
	return client.Send(ctx, req)
}
