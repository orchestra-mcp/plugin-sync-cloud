package auth

import (
	"fmt"
	"os"
	"strings"
	"sync"

	cloudsync "github.com/orchestra-mcp/plugin-sync-cloud/internal/sync"
)

// Manager handles authentication lifecycle: login, logout, env var loading,
// and credential caching.
type Manager struct {
	store  *Store
	client *cloudsync.CloudClient
	mu     sync.RWMutex
	creds  *Credentials
}

// NewManager creates a new auth manager.
func NewManager(store *Store, client *cloudsync.CloudClient) *Manager {
	return &Manager{store: store, client: client}
}

// Boot loads credentials from env vars or disk on plugin startup.
func (m *Manager) Boot() error {
	// Check env vars first.
	apiURL := os.Getenv("ORCHESTRA_API_URL")
	if apiURL == "" {
		apiURL = "http://localhost:8080"
	}
	m.client.SetBaseURL(apiURL)

	// Path 1: ORCHESTRA_TOKEN — JWT or orch_* API key (auto-detects).
	if token := os.Getenv("ORCHESTRA_TOKEN"); token != "" {
		if strings.HasPrefix(token, "orch_") {
			// API key — exchange for JWT first.
			resp, err := m.client.APIKeyExchange(token)
			if err != nil {
				return fmt.Errorf("exchange ORCHESTRA_TOKEN (API key): %w", err)
			}
			m.mu.Lock()
			m.creds = &Credentials{
				Token:    resp.Token,
				UserID:   fmt.Sprintf("%d", resp.User.ID),
				Email:    resp.User.Email,
				Name:     resp.User.Name,
				DeviceID: deviceID(),
				APIURL:   apiURL,
			}
			m.mu.Unlock()
			return nil
		}
		// JWT — validate directly.
		user, err := m.client.Me(token)
		if err != nil {
			return fmt.Errorf("validate ORCHESTRA_TOKEN: %w", err)
		}
		m.mu.Lock()
		m.creds = &Credentials{
			Token:    token,
			UserID:   fmt.Sprintf("%d", user.ID),
			Email:    user.Email,
			Name:     user.Name,
			DeviceID: deviceID(),
			APIURL:   apiURL,
		}
		m.mu.Unlock()
		return nil
	}

	// Path 2: ORCHESTRA_API_KEY (exchange for JWT).
	if apiKey := os.Getenv("ORCHESTRA_API_KEY"); apiKey != "" {
		resp, err := m.client.APIKeyExchange(apiKey)
		if err != nil {
			return fmt.Errorf("exchange ORCHESTRA_API_KEY: %w", err)
		}
		m.mu.Lock()
		m.creds = &Credentials{
			Token:    resp.Token,
			UserID:   fmt.Sprintf("%d", resp.User.ID),
			Email:    resp.User.Email,
			Name:     resp.User.Name,
			DeviceID: deviceID(),
			APIURL:   apiURL,
		}
		m.mu.Unlock()
		return nil
	}

	// Path 3: Load from ~/.orchestra/auth.json.
	creds, err := m.store.Load()
	if err != nil {
		return nil // Non-fatal: just not authenticated.
	}
	if creds == nil {
		return nil
	}

	// Update URL from env if set.
	if apiURL != creds.APIURL {
		m.client.SetBaseURL(creds.APIURL)
	} else {
		m.client.SetBaseURL(apiURL)
	}

	// Validate the stored token.
	if _, err := m.client.Me(creds.Token); err != nil {
		_ = m.store.Clear() // Token expired.
		return nil
	}

	m.mu.Lock()
	m.creds = creds
	m.mu.Unlock()
	return nil
}

// Login authenticates with email/password.
func (m *Manager) Login(email, password, apiURL string) (*Credentials, error) {
	if apiURL != "" {
		m.client.SetBaseURL(apiURL)
	}
	resp, err := m.client.Login(email, password)
	if err != nil {
		return nil, err
	}
	if resp.Requires2FA {
		return nil, fmt.Errorf("2FA required — call orchestra_login again with otp_code")
	}
	return m.finishLogin(resp, apiURL)
}

// LoginWithOTP completes 2FA authentication.
func (m *Manager) LoginWithOTP(email, code, apiURL string) (*Credentials, error) {
	if apiURL != "" {
		m.client.SetBaseURL(apiURL)
	}
	resp, err := m.client.VerifyOTP(email, code)
	if err != nil {
		return nil, err
	}
	return m.finishLogin(resp, apiURL)
}

func (m *Manager) finishLogin(resp *cloudsync.LoginResponse, apiURL string) (*Credentials, error) {
	if apiURL == "" {
		apiURL = m.client.BaseURL()
	}
	devID := deviceID()

	creds := &Credentials{
		Token:    resp.Token,
		UserID:   fmt.Sprintf("%d", resp.User.ID),
		Email:    resp.User.Email,
		Name:     resp.User.Name,
		DeviceID: devID,
		APIURL:   apiURL,
	}

	// Register device with cloud.
	hostname, _ := os.Hostname()
	_ = m.client.RegisterDevice(resp.Token, devID, hostname, "mcp")

	// Save to disk.
	if err := m.store.Save(creds); err != nil {
		return nil, fmt.Errorf("save credentials: %w", err)
	}

	m.mu.Lock()
	m.creds = creds
	m.mu.Unlock()
	return creds, nil
}

// LoginWithDeviceToken completes a device auth flow by saving the token
// and user information returned from a successful device poll.
func (m *Manager) LoginWithDeviceToken(token string, user cloudsync.UserResponse, apiURL string) (*Credentials, error) {
	if apiURL != "" {
		m.client.SetBaseURL(apiURL)
	}
	// Build a LoginResponse to reuse finishLogin.
	resp := &cloudsync.LoginResponse{
		Token: token,
		User:  user,
	}
	return m.finishLogin(resp, apiURL)
}

// Logout clears credentials.
func (m *Manager) Logout() error {
	m.mu.Lock()
	m.creds = nil
	m.mu.Unlock()
	return m.store.Clear()
}

// IsAuthenticated returns true if credentials are loaded.
func (m *Manager) IsAuthenticated() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.creds != nil && m.creds.Token != ""
}

// Creds returns the current credentials (nil if not authenticated).
func (m *Manager) Creds() *Credentials {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.creds
}

// Token returns the current JWT token.
func (m *Manager) Token() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.creds == nil {
		return ""
	}
	return m.creds.Token
}

// deviceID returns a stable device identifier.
func deviceID() string {
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "unknown"
	}
	return "mcp-" + hostname
}
