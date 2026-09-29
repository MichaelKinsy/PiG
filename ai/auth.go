package ai

// Ports packages/coding-agent/src/core/auth-storage.ts
// Credentials use Pi's JSON shape and proper-lockfile directory lock, including across Pi and PiG processes.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/MichaelKinsy/PiG/extensions/sdk/json"
	"github.com/MichaelKinsy/PiG/internal/configvalue"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
	"github.com/MichaelKinsy/PiG/internal/pilock"
	"github.com/MichaelKinsy/PiG/internal/text"
)

// AuthSource reports where auth would be sourced from.
// Mirrors upstream AuthStatus.source union.
type AuthSource string

const (
	AuthSourceStored            AuthSource = "stored"
	AuthSourceRuntime           AuthSource = "runtime"
	AuthSourceEnvironment       AuthSource = "environment"
	AuthSourceFallback          AuthSource = "fallback"
	AuthSourceModelsJSONKey     AuthSource = "models_json_key"
	AuthSourceModelsJSONCommand AuthSource = "models_json_command"
)

// AuthStatus reports whether auth is configured and where pig would source
// it from, without exposing secret values.
type AuthStatus struct {
	Configured bool
	Source     AuthSource
	Label      string
}

// CredentialType is the discriminator stored as `"type"` in auth.json.
//
// Values match upstream pi `dist/core/auth-storage.d.ts`:
//   - "api_key" for raw API keys (field: "key")
//   - "oauth"   for OAuth refresh/access tokens
//
// Files written by pig must round-trip through upstream pi and vice versa.
// Earlier PiG builds wrote "type":"api" / "apiKey", which broke parity. The
// Load() path migrates the old shape forward in place on first read.
type CredentialType string

const (
	CredentialAPIKey CredentialType = "api_key"
	CredentialOAuth  CredentialType = "oauth"

	// legacyCredentialAPIKey is the pre-parity-fix discriminator. Detected
	// during Load() and rewritten to CredentialAPIKey on next write.
	legacyCredentialAPIKey CredentialType = "api"
)

// Credential is the on-disk shape for one provider's credentials.
// We use a flat struct that covers both the api-key and oauth variants -
// fields not relevant to the type are simply zero-valued.
//
// JSON field names match upstream's typed surface so files are
// interchangeable with `pi`'s auth.json.
type Credential struct {
	Extra map[string]json.RawMessage `json:"-"`
	Type  CredentialType             `json:"type"`

	// API-key variant. Field is "key" (matches upstream); we keep an
	// alias "apiKey" on read for backwards compatibility with auth.json
	// files written by older pig builds.
	Key string `json:"key,omitempty"`

	// Env holds provider-scoped environment overrides for an API-key
	// credential. Values take precedence over the process environment when
	// resolving this credential's own `$VAR` references and when the provider
	// reads configuration env. Mirrors upstream auth-storage.ts
	// ApiKeyCredential.env.
	Env map[string]string `json:"env,omitempty"`

	// OAuth variant (used by github-copilot, anthropic, etc.)
	Refresh string `json:"refresh,omitempty"`
	Access  string `json:"access,omitempty"`
	// Expires is the truncated integer projection of expires in milliseconds since the epoch. ExpiresMillis returns Pi's exact value, including a fraction or an absent property.
	// Expiry decisions must use ExpiresMillis. SetExpiresMillis stores a fraction; assigning Expires replaces any retained exact value.
	Expires          int64  `json:"expires,omitempty"`
	ProjectID        string `json:"projectId,omitempty"`
	AccountID        string `json:"accountId,omitempty"`
	EnterpriseDomain string `json:"enterpriseUrl,omitempty"`
	// Scope is the granted OAuth scope some flows (Radius) return and persist.
	Scope string `json:"scope,omitempty"`
	// AvailableModelIDs retains the account's optional picker filter, including an explicit empty list or malformed external value.
	AvailableModelIDs json.RawMessage `json:"availableModelIds,omitempty"`
	// GatewayConfig is the Radius gateway catalog that pre-ModelsStore Radius
	// builds cached on the credential. It is read once to import that catalog.
	GatewayConfig json.RawMessage `json:"gatewayConfig,omitempty"`

	expiry credentialExpiry
}

// rawCredential mirrors Credential but also accepts the legacy `apiKey`
// JSON field. Used only during Load() to migrate old files in place.
type rawCredential struct {
	Extra             map[string]json.RawMessage `json:"-"`
	Type              CredentialType             `json:"type"`
	Key               string                     `json:"key,omitempty"`
	LegacyAPIKey      string                     `json:"apiKey,omitempty"`
	Env               map[string]string          `json:"env,omitempty"`
	Refresh           string                     `json:"refresh,omitempty"`
	Access            string                     `json:"access,omitempty"`
	Expires           int64                      `json:"expires,omitempty"`
	ProjectID         string                     `json:"projectId,omitempty"`
	AccountID         string                     `json:"accountId,omitempty"`
	EnterpriseDomain  string                     `json:"enterpriseUrl,omitempty"`
	Scope             string                     `json:"scope,omitempty"`
	GatewayConfig     json.RawMessage            `json:"gatewayConfig,omitempty"`
	AvailableModelIDs json.RawMessage            `json:"availableModelIds,omitempty"`

	expiry credentialExpiry
}

func (r rawCredential) normalize() Credential {
	c := Credential{
		Extra:             cloneCredentialExtra(r.Extra),
		Type:              r.Type,
		Key:               r.Key,
		Env:               r.Env,
		Refresh:           r.Refresh,
		Access:            r.Access,
		Expires:           r.Expires,
		ProjectID:         r.ProjectID,
		AccountID:         r.AccountID,
		EnterpriseDomain:  r.EnterpriseDomain,
		Scope:             r.Scope,
		GatewayConfig:     r.GatewayConfig,
		AvailableModelIDs: r.AvailableModelIDs,
		expiry:            r.expiry,
	}
	if c.Type == legacyCredentialAPIKey {
		c.Type = CredentialAPIKey
	}
	if c.Key == "" && r.LegacyAPIKey != "" {
		c.Key = r.LegacyAPIKey
	}
	return c
}

// AuthStorage is the persistent credential store at <agentDir>/auth.json.
type AuthStorage struct {
	path string
	mu   authMutex // cancellable process-local guard around mutations
	read *authReadState
}

// NewAuthStorage opens the credential store and materializes a missing auth.json as {} with mode 0600. The parent directory uses mode 0700. Existing bytes and permissions remain unchanged; a failed initial load preserves the last valid snapshot. Instances of the first auth path share that snapshot.
func NewAuthStorage(path string) (*AuthStorage, error) {
	if path == "" {
		return nil, errors.New("auth: empty path")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("auth: ensure dir: %w", err)
	}
	store := &AuthStorage{path: path, read: authReadStateForPath(path)}
	store.reloadInitialSnapshot()
	return store, nil
}

// Path returns the auth.json file path.
func (a *AuthStorage) Path() string { return a.path }

// Load reads all credentials, creating an empty file if it no longer exists.
func (a *AuthStorage) Load() (map[string]Credential, error) {
	return a.load(context.Background())
}

func (a *AuthStorage) load(ctx context.Context) (map[string]Credential, error) {
	creds, err := a.readLatest(ctx)
	if err != nil {
		return nil, err
	}
	return cloneAuthCredentials(creds.values), nil
}

func cloneAuthCredentials(creds map[string]Credential) map[string]Credential {
	out := make(map[string]Credential, len(creds))
	for provider, cred := range creds {
		out[provider] = cloneCredential(cred)
	}
	return out
}

// credential returns one provider's stored credential from the latest snapshot.
func (a *AuthStorage) credential(provider string) (Credential, bool, error) {
	creds, err := a.readLatest(context.Background())
	if err != nil {
		return Credential{}, false, err
	}
	cred, ok := creds.values[provider]
	return cloneCredential(cred), ok, nil
}

// readAuthFile reads auth.json. Tests replace it to count file reads.
var readAuthFile = os.ReadFile

func (a *AuthStorage) loadLocked() (*authStorageData, error) {
	data, err := readAuthFile(a.path)
	if errors.Is(err, fs.ErrNotExist) {
		return newAuthStorageData(nil), nil
	}
	if err != nil {
		return nil, fmt.Errorf("auth: read: %w", err)
	}
	if len(data) == 0 {
		return newAuthStorageData(nil), nil
	}
	// Preserve UTF-16 provider-key identity before building the map; JSON.parse keeps lone surrogates distinct from U+FFFD.
	data = text.StripBomBytes(data)
	raw := map[string]rawCredential{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("auth: parse: %w", err)
	}
	// Legacy shapes are normalized in memory only: upstream never writes on
	// read, and the next write stores the current shape.
	creds := make(map[string]Credential, len(raw))
	for k, r := range raw {
		creds[k] = r.normalize()
	}
	order, err := authStorageObjectKeys(data)
	if err != nil {
		return nil, fmt.Errorf("auth: parse: %w", err)
	}
	return &authStorageData{values: creds, order: order}, nil
}

// Get returns the credential for a provider, if present.
//
// For API-key credentials the Key field is passed through
// configvalue.Resolve so that auth.json values like "$OPENAI_API_KEY"
// (explicit env reference) or "!op read op://Personal/openai/key" (shell
// command) become the runtime value. Bare names are literals (v0.78.1).
// Mirrors upstream auth-storage.ts which calls resolveConfigValue on the
// API key on the read path. The on-disk value is left untouched.
func (a *AuthStorage) Get(provider string) (Credential, bool, error) {
	c, ok, err := a.credential(provider)
	if err != nil {
		return Credential{}, false, err
	}
	if ok && c.Type == CredentialAPIKey && c.Key != "" {
		c.Key = configvalue.Resolve(c.Key, c.Env)
	}
	return c, ok, nil
}

// GetProviderEnv returns a copy of the provider-scoped environment overrides
// for an API-key credential, or nil when none are set. Mirrors upstream
// auth-storage.ts getProviderEnv.
func (a *AuthStorage) GetProviderEnv(provider string) (map[string]string, error) {
	c, ok, err := a.credential(provider)
	if err != nil {
		return nil, err
	}
	if !ok || c.Type != CredentialAPIKey || len(c.Env) == 0 {
		return nil, nil
	}
	return c.Env, nil
}

// GetRaw returns the credential without applying configvalue.Resolve to
// the Key field. Use this when you need the on-disk value (e.g. for
// rewriting auth.json or for OAuth flows where Refresh/Access tokens
// must round-trip verbatim).
func (a *AuthStorage) GetRaw(provider string) (Credential, bool, error) {
	return a.credential(provider)
}

// GetAuthStatus reports whether auth is configured for provider without
// exposing credential values or refreshing OAuth tokens.
func (a *AuthStorage) GetAuthStatus(provider string) AuthStatus {
	cred, ok, err := a.GetRaw(provider)
	if err == nil && ok {
		_ = cred
		return AuthStatus{Configured: true, Source: AuthSourceStored}
	}
	if key := firstAuthEnvKey(provider); key != "" {
		return AuthStatus{Configured: false, Source: AuthSourceEnvironment, Label: key}
	}
	return AuthStatus{Configured: false}
}

// Set writes (or replaces) the credential for a provider.
// Acquires an exclusive file lock during the read-modify-write cycle so
// concurrent pi instances refreshing the same OAuth token don't clobber
// each other.
func (a *AuthStorage) Set(provider string, cred Credential) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withFileLock(context.Background(), false, func(lock *pilock.Lock) error {
		creds, err := a.loadLocked()
		if err != nil {
			return err
		}
		creds.set(provider, cred)
		return a.writeLocked(creds, lock)
	})
}

// Update mutates the credential for a provider under the lock. The mutator
// receives the current credential (zero value if absent) and the boolean
// indicates whether it existed. Returning an error aborts the write.
func (a *AuthStorage) Update(provider string, mutate func(cur Credential, exists bool) (Credential, error)) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.withFileLock(context.Background(), false, func(lock *pilock.Lock) error {
		creds, err := a.loadLocked()
		if err != nil {
			return err
		}
		cur, ok := creds.values[provider]
		next, err := mutate(cur, ok)
		if err != nil {
			return err
		}
		creds.set(provider, next)
		return a.writeLocked(creds, lock)
	})
}

// Delete removes the credential for a provider (no-op if absent). Cancellation aborts process-local or file-lock acquisition and prevents a pending write without releasing another operation's ownership.
func (a *AuthStorage) Delete(ctx context.Context, provider string) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if err := a.mu.LockContext(ctx); err != nil {
		return err
	}
	defer a.mu.Unlock()
	return a.withFileLock(ctx, true, func(lock *pilock.Lock) error {
		creds, err := a.loadLocked()
		if err != nil {
			return err
		}
		creds.delete(provider)
		return a.writeLocked(creds, lock)
	})
}

func (a *AuthStorage) writeLocked(creds *authStorageData, lock *pilock.Lock) error {
	a.read.mu.Lock()
	a.read.revision = ""
	a.read.mu.Unlock()
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return fmt.Errorf("auth: marshal: %w", err)
	}
	if err := lock.Check(); err != nil {
		return err
	}
	// Pi applies the mode only at creation; existing modes, ACLs and symlinks remain intact.
	if err := os.WriteFile(a.path, data, 0o600); err != nil {
		return fmt.Errorf("auth: write: %w", err)
	}
	if err := lock.Check(); err != nil {
		return err
	}
	a.read.mu.Lock()
	a.read.creds = &authStorageData{values: cloneAuthCredentials(creds.values), order: slices.Clone(creds.order)}
	a.read.mu.Unlock()
	return nil
}

func (a *AuthStorage) ensureFileExists() error {
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return fmt.Errorf("auth: ensure dir: %w", err)
	}
	// Exclusive creation preserves existing bytes, modes, ACLs and symlinks, including when another process creates the file concurrently.
	file, err := ownerfile.CreateNew(a.path)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth: create: %w", err)
	}
	_, writeErr := file.WriteString("{}")
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("auth: initialize: %w", err)
	}
	return nil
}

// File operations share acquisition and release boundaries; one coalesced reload owns one lease.
var (
	acquireAuthFileLock = pilock.Acquire
	releaseAuthFileLock = (*pilock.Lock).Release
)

// withFileLock serializes reads and writes with Pi's synchronous or cancellable auth lock contract.
func (a *AuthStorage) withFileLock(ctx context.Context, asynchronous bool, fn func(*pilock.Lock) error) (err error) {
	if err := context.Cause(ctx); err != nil {
		return err
	}
	// Pi's FileAuthStorageBackend ensures the file before acquiring either lock.
	if err := a.ensureFileExists(); err != nil {
		return err
	}
	var lock *pilock.Lock
	if asynchronous {
		lock, err = acquireAuthFileLock(ctx, a.path)
	} else {
		lock, err = pilock.AcquireSync(a.path)
	}
	if err != nil {
		return fmt.Errorf("auth: acquire lock: %w", err)
	}
	defer func() { err = errors.Join(err, releaseAuthFileLock(lock)) }()
	if err := lock.Check(); err != nil {
		return err
	}
	return fn(lock)
}

func firstAuthEnvKey(provider string) string {
	if keys := FindEnvKeys(provider, nil); len(keys) > 0 {
		return keys[0]
	}
	return ""
}
