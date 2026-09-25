package codingagent

// session_share.go ports upstream modes/interactive/session-share.ts. It
// exports the active branch with the pi.share presentation entry, then uploads
// that JSONL artifact only when the user runs /share.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/MichaelKinsy/PiG/agent"
	"github.com/MichaelKinsy/PiG/tui"
)

// ShareTool is one tool schema recorded in the pi.share entry.
type ShareTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Parameters  any    `json:"parameters,omitempty"`
}

// ShareState is the agent state the pi.share entry records: upstream
// session.state.systemPrompt and session.state.tools.
type ShareState struct {
	SystemPrompt string
	Tools        []ShareTool
}

// NewShareState captures the system prompt and the active agent tools.
func NewShareState(systemPrompt string, tools []agent.AgentTool) ShareState {
	state := ShareState{SystemPrompt: systemPrompt, Tools: make([]ShareTool, 0, len(tools))}
	for _, tool := range tools {
		schema := tool.Schema()
		shareTool := ShareTool{Name: tool.Name(), Description: schema.Description}
		if schema.Parameters != nil {
			shareTool.Parameters = schema.Parameters
		}
		state.Tools = append(state.Tools, shareTool)
	}
	return state
}

type shareEntryData struct {
	SystemPrompt string      `json:"systemPrompt"`
	Tools        []ShareTool `json:"tools"`
}

type shareEntry struct {
	Type       string         `json:"type"`
	CustomType string         `json:"customType"`
	ID         string         `json:"id"`
	ParentID   *string        `json:"parentId"`
	Timestamp  string         `json:"timestamp"`
	Data       shareEntryData `json:"data"`
}

// CreateShareTrailingEntries returns the trailing pi.share entry carrying the
// system prompt and tool schemas for the session viewer. Mirrors upstream
// createShareTrailingEntries.
func CreateShareTrailingEntries(state ShareState, parentID *string, timestamp string) []any {
	return []any{shareEntry{
		Type:       "custom",
		CustomType: "pi.share",
		ID:         uuid.NewString()[:8],
		ParentID:   parentID,
		Timestamp:  timestamp,
		Data:       shareEntryData(state),
	}}
}

// shareTrailingEntries adapts CreateShareTrailingEntries to TrailingEntries.
func shareTrailingEntries(state ShareState) TrailingEntries {
	return func(parentID *string, timestamp string) []any {
		return CreateShareTrailingEntries(state, parentID, timestamp)
	}
}

// ExportSessionForShare writes the current branch with the export-only
// pi.share presentation entry. Mirrors upstream exportSessionForShare.
func ExportSessionForShare(filePath string, session *Session, state ShareState) (string, error) {
	return ExportSessionToJsonl(session, filePath, shareTrailingEntries(state))
}

// pig divergence (D64): PiG uploads explicitly shared sessions to its own
// pi-in-go.dev gateway instead of Earendil's Radius gateway.
const defaultShareGatewayURL = "https://pi-in-go.dev/v1/artifacts?visibility=unlisted&title=PiG+session"

const (
	// pig divergence (D64): bound PiG gateway upload time.
	shareUploadTimeout = 30 * time.Second
	// pig divergence (D64): bound PiG gateway response memory.
	shareResponseLimit = 1 << 20
	sharePrivacyNotice = "Privacy: /share uploads the current session to pi-in-go.dev, including prompts, model responses, tool calls, and tool output. Anyone with the link can read it. The service deletes it after 30 days."
)

func shareGatewayURL() string {
	if configured := strings.TrimSpace(os.Getenv("PI_SHARE_GATEWAY_URL")); configured != "" {
		return configured
	}
	return defaultShareGatewayURL
}

type shareUploadResponse struct {
	Artifact *struct {
		CanonicalURL string `json:"canonical_url"`
	} `json:"artifact"`
	Error string `json:"error"`
}

// uploadShareArtifact uploads one bounded file and accepts only a canonical URL
// on the gateway's own origin. The Worker enforces the content and storage
// limits; this client timeout bounds the interactive operation.
func uploadShareArtifact(ctx context.Context, client *http.Client, gatewayURL, filePath string) (string, error) {
	gateway, err := url.Parse(gatewayURL)
	if err != nil || gateway.Scheme == "" || gateway.Host == "" {
		return "", errors.New("invalid share gateway URL")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gateway.String(), file)
	if err != nil {
		return "", err
	}
	req.ContentLength = info.Size()
	req.Header.Set("Content-Type", "application/x-ndjson")
	req.Header.Set("Accept", "application/json")
	response, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, shareResponseLimit+1))
	if err != nil {
		return "", err
	}
	if len(body) > shareResponseLimit {
		return "", errors.New("share gateway response is too large")
	}
	var result shareUploadResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("share gateway returned HTTP %d with unreadable JSON", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || result.Artifact == nil {
		message := strings.TrimSpace(result.Error)
		if message == "" {
			message = response.Status
		}
		return "", errors.New(message)
	}
	canonical, err := url.Parse(result.Artifact.CanonicalURL)
	if err != nil || canonical.Scheme != gateway.Scheme || canonical.Host != gateway.Host || canonical.User != nil {
		return "", errors.New("share gateway returned a URL on a different origin")
	}
	return canonical.String(), nil
}

// shareSession performs the explicit /share export and upload. Each invocation
// owns its temporary directory, so concurrent shares cannot overwrite one
// another.
func shareSession(ctx context.Context, session *Session, state ShareState, gatewayURL string, client *http.Client, showStatus func(string)) (string, error) {
	// pig divergence (D64): disclose PiG-platform retention and link access before upload.
	showStatus(sharePrivacyNotice)
	shareDir, err := os.MkdirTemp("", "pig-share-")
	if err != nil {
		return "", fmt.Errorf("Failed to export session: %w", err)
	}
	defer func() { _ = os.RemoveAll(shareDir) }()

	jsonlPath := filepath.Join(shareDir, "session.jsonl")
	if _, err := ExportSessionForShare(jsonlPath, session, state); err != nil {
		return "", fmt.Errorf("Failed to export session: %w", err)
	}
	if client == nil {
		client = &http.Client{Timeout: shareUploadTimeout}
	}
	shareURL, err := uploadShareArtifact(ctx, client, gatewayURL, jsonlPath)
	if err != nil {
		return "", fmt.Errorf("Failed to upload session: %w", err)
	}
	return "Share URL: " + tui.Hyperlink(shareURL, shareURL), nil
}
