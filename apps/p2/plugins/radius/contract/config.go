package contract

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Config is the brain's `radius.json` (`RadiusBrainConfig`): the gateway, a stable server ID, and an OAuth credential from
// `p2 radius-login`.
type Config struct {
	Gateway  string
	ServerID string
	// PublicOrigin is the brain's own Radius origin (`https://<label>.rdtun.com`), as last seen on a relayed request for its
	// UI. Saved so forward links are known after a restart before anyone opens Pi through Radius again.
	PublicOrigin string
	// ShellPage is this brain's page in the Radius shell (`<shell>/pi/brains/<organization>/<server>`), as last seen on a
	// relayed request for its UI. People sessions are shared with by email open them there, signed in to Radius.
	ShellPage  string
	Credential ai.OAuthCredentials
}

// ConfigPath is the file of the brain's configuration in directory.
func ConfigPath(directory string) string { return filepath.Join(directory, "radius.json") }

// ReadConfig reads the configuration; nil when there is none (ENOENT).
func ReadConfig(directory string) (*Config, error) {
	text, err := os.ReadFile(ConfigPath(directory))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var value struct {
		Gateway      *string          `json:"gateway"`
		ServerID     *string          `json:"serverId"`
		PublicOrigin *string          `json:"publicOrigin"`
		ShellPage    *string          `json:"shellPage"`
		Credential   *json.RawMessage `json:"credential"`
	}
	if err := json.Unmarshal(text, &value); err != nil {
		return nil, err
	}
	var kind struct {
		Type string `json:"type"`
	}
	if value.Gateway == nil || value.ServerID == nil || value.Credential == nil || json.Unmarshal(*value.Credential, &kind) != nil || kind.Type != "oauth" {
		return nil, errors.New("Invalid radius.json")
	}
	config := &Config{Gateway: *value.Gateway, ServerID: *value.ServerID}
	if value.PublicOrigin != nil {
		config.PublicOrigin = *value.PublicOrigin
	}
	if value.ShellPage != nil {
		config.ShellPage = *value.ShellPage
	}
	if err := json.Unmarshal(*value.Credential, &config.Credential); err != nil {
		return nil, errors.New("Invalid radius.json")
	}
	return config, nil
}

// WriteConfig writes the configuration (0600) through a temporary file, so a reader never sees half of it.
func WriteConfig(directory string, config Config) error {
	credential, err := chordjson.Marshal(config.Credential)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	//perf:ok the credential's own fields, which `type` joins: written once per sign-in (cold)
	if err := json.Unmarshal(credential, &fields); err != nil { //portlint:allow jsonclone a typed value converted to the object `type` joins, cold
		return err
	}
	fields["type"] = json.RawMessage(`"oauth"`)
	stored := struct {
		Gateway      string                     `json:"gateway"`
		ServerID     string                     `json:"serverId"`
		PublicOrigin string                     `json:"publicOrigin,omitempty"`
		ShellPage    string                     `json:"shellPage,omitempty"`
		Credential   map[string]json.RawMessage `json:"credential"`
	}{config.Gateway, config.ServerID, config.PublicOrigin, config.ShellPage, fields}
	// JSON.stringify(config, null, "\t"): no HTML escaping, then the tab indentation.
	compact, err := chordjson.Marshal(stored)
	if err != nil {
		return err
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, compact, "", "\t"); err != nil {
		return err
	}
	encoded := indented.Bytes()
	path := ConfigPath(directory)
	if err := os.WriteFile(path+".tmp", append(encoded, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// GatewayFromEnv is the gateway for new logins: `PI_RADIUS_GATEWAY`, as in the coding agent, else production.
func GatewayFromEnv() string {
	gateway, ok := os.LookupEnv("PI_RADIUS_GATEWAY")
	if !ok {
		gateway = ai.DefaultRadiusGateway
	}
	return ai.NormalizeRadiusGatewayURL(gateway)
}

// NewServerID is a new random server ID (`randomUUID`).
func NewServerID() string {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		panic(err)
	}
	random[6] = random[6]&0x0f | 0x40
	random[8] = random[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", random[0:4], random[4:6], random[6:8], random[8:10], random[10:])
}

func radiusOAuth(gateway string) *ai.RadiusOAuth {
	return ai.CreateRadiusOAuth(ai.RadiusOAuthOptions{ID: "radius", Name: "Radius", Gateway: gateway})
}

// Login signs in to Radius by device code (works on a phone or over SSH): onDeviceCode shows the code.
func Login(ctx context.Context, gateway string, onDeviceCode func(url, code string)) (ai.OAuthCredentials, error) {
	return radiusOAuth(gateway).LoginContext(ctx, ai.OAuthLoginCallbacks{
		OnSelect: func(ai.OAuthSelectPrompt) (string, error) { return ai.RadiusLoginMethodDeviceCode, nil },
		OnDeviceCode: func(info ai.OAuthDeviceCodeInfo) {
			onDeviceCode(info.VerificationURI, info.UserCode)
		},
	})
}

// refreshMargin is how long before it expires a credential is refreshed.
const refreshMargin = 5 * time.Minute

func expiresSoon(credential ai.OAuthCredentials) bool {
	return credential.ExpiresMillis()-float64(time.Now().UnixMilli()) <= float64(refreshMargin.Milliseconds())
}
