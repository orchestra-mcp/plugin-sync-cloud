// Package sync implements the cloud sync engine for pushing local data to the
// Orchestra cloud API.
package sync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// CloudClient makes HTTP calls to the Orchestra cloud API (apps/web).
type CloudClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewCloudClient creates a new cloud API client.
func NewCloudClient(baseURL string) *CloudClient {
	return &CloudClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetBaseURL updates the base URL.
func (cc *CloudClient) SetBaseURL(url string) {
	cc.baseURL = url
}

// BaseURL returns the current base URL.
func (cc *CloudClient) BaseURL() string {
	return cc.baseURL
}

// --- Auth ---

// LoginRequest is the body for POST /api/auth/login.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// LoginResponse is the response from POST /api/auth/login.
type LoginResponse struct {
	Token       string       `json:"token"`
	User        UserResponse `json:"user"`
	Requires2FA bool         `json:"requires_2fa"`
	TempToken   string       `json:"temp_token,omitempty"`
}

// UserResponse is a user object from the cloud API.
type UserResponse struct {
	ID    uint   `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// OTPVerifyRequest is the body for POST /api/auth/otp/verify.
type OTPVerifyRequest struct {
	Email string `json:"email"`
	Code  string `json:"code"`
	Type  string `json:"type"`
}

// Login authenticates with email/password.
func (cc *CloudClient) Login(email, password string) (*LoginResponse, error) {
	body := LoginRequest{Email: email, Password: password}
	var resp LoginResponse
	if err := cc.post("/api/auth/login", "", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// VerifyOTP verifies a 2FA code and returns a full login response.
func (cc *CloudClient) VerifyOTP(email, code string) (*LoginResponse, error) {
	body := OTPVerifyRequest{Email: email, Code: code, Type: "login"}
	var resp LoginResponse
	if err := cc.post("/api/auth/otp/verify", "", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Me validates a token and returns the current user.
func (cc *CloudClient) Me(token string) (*UserResponse, error) {
	var resp UserResponse
	if err := cc.get("/api/auth/me", token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// APIKeyExchange exchanges an API key for a JWT token.
func (cc *CloudClient) APIKeyExchange(apiKey string) (*LoginResponse, error) {
	req, err := http.NewRequest("POST", cc.baseURL+"/api/auth/api-key-exchange", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-API-Key", apiKey)
	resp, err := cc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api key exchange: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, cc.readError(resp)
	}
	var result LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &result, nil
}

// --- Device Auth ---

// DeviceAuthRequest is the body for POST /api/auth/device/request.
type DeviceAuthRequest struct {
	DeviceName string `json:"device_name"`
}

// DeviceAuthResponse is the response from POST /api/auth/device/request.
type DeviceAuthResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURL string `json:"verification_url"`
	ExpiresIn       int    `json:"expires_in"`
}

// DevicePollRequest is the body for POST /api/auth/device/poll.
type DevicePollRequest struct {
	DeviceCode string `json:"device_code"`
}

// DevicePollResponse is the response from POST /api/auth/device/poll.
type DevicePollResponse struct {
	Status string       `json:"status"` // "pending" or "approved"
	Token  string       `json:"token,omitempty"`
	User   UserResponse `json:"user,omitempty"`
}

// DeviceRequest initiates a device auth flow. No auth token is needed.
func (cc *CloudClient) DeviceRequest(deviceName string) (*DeviceAuthResponse, error) {
	body := DeviceAuthRequest{DeviceName: deviceName}
	var resp DeviceAuthResponse
	if err := cc.post("/api/auth/device/request", "", body, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// DevicePoll polls for device auth approval. Returns the response and HTTP
// status code (200 = approved, 202 = pending, 404 = expired/invalid).
// No auth token is needed.
func (cc *CloudClient) DevicePoll(deviceCode string) (*DevicePollResponse, int, error) {
	body := DevicePollRequest{DeviceCode: deviceCode}
	data, err := json.Marshal(body)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest("POST", cc.baseURL+"/api/auth/device/poll", bytes.NewReader(data))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cc.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("request /api/auth/device/poll: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return nil, resp.StatusCode, cc.readError(resp)
	}

	var result DevicePollResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("decode response: %w", err)
	}
	return &result, resp.StatusCode, nil
}

// --- Sync ---

// SyncRecord is a single entity change to push.
type SyncRecord struct {
	EntityType     string          `json:"entity_type"`
	EntityID       string          `json:"entity_id"`
	Action         string          `json:"action"`
	Payload        json.RawMessage `json:"payload"`
	Version        int64           `json:"version"`
	IdempotencyKey string          `json:"idempotency_key,omitempty"`
	TeamID         string          `json:"team_id,omitempty"`
}

// PushRequest is the body for POST /api/sync/push.
type PushRequest struct {
	DeviceID string       `json:"device_id"`
	TunnelID string       `json:"tunnel_id,omitempty"`
	Records  []SyncRecord `json:"records"`
}

// PushResult is the per-record result from a push.
type PushResult struct {
	EntityID string `json:"entity_id"`
	Status   string `json:"status"` // applied | skipped | error
	Error    string `json:"error,omitempty"`
}

// PushResponse is the response from POST /api/sync/push.
type PushResponse struct {
	Results []PushResult `json:"results"`
}

// Push sends sync records to the cloud.
func (cc *CloudClient) Push(token string, req PushRequest) (*PushResponse, error) {
	var resp PushResponse
	if err := cc.post("/api/sync/push", token, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// RegisterDevice registers this device with the cloud.
func (cc *CloudClient) RegisterDevice(token, deviceID, name, platform string) error {
	body := map[string]string{
		"device_id": deviceID,
		"name":      name,
		"platform":  platform,
	}
	var resp map[string]interface{}
	return cc.post("/api/sync/devices/register", token, body, &resp)
}

// StatusResponse is the response from GET /api/sync/status.
type StatusResponse struct {
	LastSyncAt   *time.Time      `json:"last_sync_at"`
	PendingCount int64           `json:"pending_count"`
	Devices      json.RawMessage `json:"devices"`
}

// SyncStatus gets the current sync status from the cloud.
func (cc *CloudClient) SyncStatus(token, deviceID string) (*StatusResponse, error) {
	var resp StatusResponse
	path := "/api/sync/status"
	if deviceID != "" {
		path += "?device_id=" + deviceID
	}
	if err := cc.get(path, token, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// --- HTTP helpers ---

func (cc *CloudClient) post(path, token string, body interface{}, result interface{}) error {
	data, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	req, err := http.NewRequest("POST", cc.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := cc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return cc.readError(resp)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (cc *CloudClient) get(path, token string, result interface{}) error {
	req, err := http.NewRequest("GET", cc.baseURL+path, nil)
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := cc.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return cc.readError(resp)
	}
	if result != nil {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (cc *CloudClient) readError(resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	var errResp struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &errResp) == nil && (errResp.Error != "" || errResp.Message != "") {
		msg := errResp.Message
		if msg == "" {
			msg = errResp.Error
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, msg)
	}
	return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
}
