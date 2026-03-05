package tools

import (
	"context"
	"fmt"
	"net/http"
	"os"

	pluginv1 "github.com/orchestra-mcp/gen-go/orchestra/plugin/v1"
	"github.com/orchestra-mcp/sdk-go/helpers"
	"google.golang.org/protobuf/types/known/structpb"
)

// LoginSchema returns the JSON Schema for the orchestra_login tool.
func LoginSchema() *structpb.Struct {
	s, _ := structpb.NewStruct(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"email": map[string]any{
				"type":        "string",
				"description": "Email address for your Orchestra account",
			},
			"password": map[string]any{
				"type":        "string",
				"description": "Password for your Orchestra account",
			},
			"otp_code": map[string]any{
				"type":        "string",
				"description": "2FA code (if 2FA is enabled on your account)",
			},
			"device_code": map[string]any{
				"type":        "string",
				"description": "Device code from a previous device auth request (used to poll for approval)",
			},
			"api_url": map[string]any{
				"type":        "string",
				"description": "Orchestra cloud API URL (default: http://localhost:8080)",
			},
		},
	})
	return s
}

// LoginHandler handles the orchestra_login tool.
func LoginHandler(sp PluginAccessor) ToolHandler {
	return func(ctx context.Context, req *pluginv1.ToolRequest) (*pluginv1.ToolResponse, error) {
		email := helpers.GetString(req.Arguments, "email")
		password := helpers.GetString(req.Arguments, "password")
		otpCode := helpers.GetString(req.Arguments, "otp_code")
		deviceCode := helpers.GetString(req.Arguments, "device_code")
		apiURL := helpers.GetString(req.Arguments, "api_url")

		authMgr := sp.AuthManager()
		cc := sp.CloudClient()

		// Resolve API URL for device auth flows (email/password flows
		// handle this inside the auth manager).
		if apiURL != "" {
			cc.SetBaseURL(apiURL)
		}

		// --- Device auth: poll mode ---
		if deviceCode != "" && email == "" {
			pollResp, statusCode, err := cc.DevicePoll(deviceCode)
			if err != nil {
				if statusCode == http.StatusNotFound {
					return helpers.ErrorResult("device_auth_expired", "Device code has expired. Start a new login flow by calling orchestra_login with no arguments."), nil
				}
				return helpers.ErrorResult("device_auth_error", err.Error()), nil
			}
			if statusCode == http.StatusAccepted || pollResp.Status == "pending" {
				result, _ := structpb.NewStruct(map[string]any{
					"status":  "pending",
					"message": "Waiting for approval... Call orchestra_login again with device_code in a few seconds.",
				})
				return &pluginv1.ToolResponse{Success: true, Result: result}, nil
			}
			// Approved — save credentials and start sync.
			creds, err := authMgr.LoginWithDeviceToken(pollResp.Token, pollResp.User, apiURL)
			if err != nil {
				return helpers.ErrorResult("auth_error", err.Error()), nil
			}
			return loginSuccess(ctx, sp, creds.Token, creds.Email, creds.Name, creds.UserID, creds.DeviceID, creds.TeamID, creds.APIURL)
		}

		// --- Device auth: initiate mode (no email, no device_code) ---
		if email == "" {
			devResp, err := cc.DeviceRequest("Orchestra MCP")
			if err != nil {
				return helpers.ErrorResult("device_auth_error", err.Error()), nil
			}
			// Use ORCHESTRA_APP_URL for the browser URL (frontend), not the API URL.
			appURL := os.Getenv("ORCHESTRA_APP_URL")
			if appURL == "" {
				appURL = "http://localhost:3000"
			}
			fullURL := fmt.Sprintf("%s/cli-auth?code=%s", appURL, devResp.UserCode)
			msg := fmt.Sprintf(
				"Open this URL in your browser to authenticate:\n\n%s\n\nYour code: %s\n\nAfter approving, call orchestra_login again with device_code: \"%s\"",
				fullURL, devResp.UserCode, devResp.DeviceCode,
			)
			result, _ := structpb.NewStruct(map[string]any{
				"status":           "device_auth_started",
				"verification_url": fullURL,
				"user_code":        devResp.UserCode,
				"device_code":      devResp.DeviceCode,
				"expires_in":       devResp.ExpiresIn,
				"message":          msg,
			})
			return &pluginv1.ToolResponse{Success: true, Result: result}, nil
		}

		// --- Email/password flow (existing) ---
		if otpCode != "" {
			creds, err := authMgr.LoginWithOTP(email, otpCode, apiURL)
			if err != nil {
				return helpers.ErrorResult("auth_error", err.Error()), nil
			}
			return loginSuccess(ctx, sp, creds.Token, creds.Email, creds.Name, creds.UserID, creds.DeviceID, creds.TeamID, creds.APIURL)
		}

		if password == "" {
			return helpers.ErrorResult("validation_error", "password is required when using email login"), nil
		}
		creds, err := authMgr.Login(email, password, apiURL)
		if err != nil {
			return helpers.ErrorResult("auth_error", err.Error()), nil
		}
		return loginSuccess(ctx, sp, creds.Token, creds.Email, creds.Name, creds.UserID, creds.DeviceID, creds.TeamID, creds.APIURL)
	}
}

func loginSuccess(ctx context.Context, sp PluginAccessor, token, email, name, userID, deviceID, teamID, apiURL string) (*pluginv1.ToolResponse, error) {
	// Start sync engine with new credentials.
	if engine := sp.SyncEngine(); engine != nil {
		engine.SetAuth(token, teamID, deviceID)
		engine.Start(ctx)
	}

	result, _ := structpb.NewStruct(map[string]any{
		"status":    "logged_in",
		"email":     email,
		"name":      name,
		"user_id":   userID,
		"device_id": deviceID,
		"api_url":   apiURL,
		"message":   fmt.Sprintf("Logged in as %s (%s). Sync is now active.", name, email),
	})

	return &pluginv1.ToolResponse{
		Success: true,
		Result:  result,
	}, nil
}
