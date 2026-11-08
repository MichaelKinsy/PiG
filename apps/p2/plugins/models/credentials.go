package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"syscall"

	"github.com/MichaelKinsy/PiG/ai"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
)

// Ports apps/p2/plugins/models/credentials.ts: CredentialStore semantics and validation adapted from Pi's
// auth-storage.ts. Locking lives in runtime.LockedFile.

func credentialsError(message string) error { return errors.New(message) }

// parseCredentialsValue validates auth.json's value: an object of credentials. An api_key credential has an optional
// string `key` and an optional `env` object of strings; an oauth one string `access` and `refresh` and a finite number
// `expires`.
func parseCredentialsValue(value any) (*chordjson.Object, error) {
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil, credentialsError("Invalid auth.json: expected object")
	}
	for _, entry := range object.All() {
		credential, ok := entry.(*chordjson.Object)
		if !ok {
			return nil, credentialsError("Invalid auth.json credential")
		}
		switch credential.Value("type") {
		case "api_key":
			keyOK := true
			if key, present := credential.Get("key"); present {
				_, isString := key.(string)
				keyOK = isString
			}
			envOK := true
			if env, present := credential.Get("env"); present {
				envObject, isObject := env.(*chordjson.Object)
				envOK = isObject
				if isObject {
					for _, each := range envObject.All() {
						if _, isString := each.(string); !isString {
							envOK = false
						}
					}
				}
			}
			if keyOK && envOK {
				continue
			}
		case "oauth":
			_, access := credential.Value("access").(string)
			_, refresh := credential.Value("refresh").(string)
			expires, isNumber := credential.Value("expires").(float64)
			if access && refresh && isNumber && !math.IsNaN(expires) && !math.IsInf(expires, 0) {
				continue
			}
		}
		return nil, credentialsError("Invalid auth.json credential")
	}
	return object, nil
}

func parseCredentials(content *string) (*chordjson.Object, error) {
	text := "{}"
	if content != nil {
		text = *content
	}
	value, err := chordjson.Decode([]byte(p2runtime.StripBOM(text)))
	if err != nil {
		return nil, credentialsError("Invalid auth.json syntax")
	}
	return parseCredentialsValue(value)
}

// credentialOf is a stored entry as the typed credential.
func credentialOf(entry any) (*ai.Credential, error) {
	encoded, err := json.Marshal(entry)
	if err != nil {
		return nil, err
	}
	var credential ai.Credential
	//perf:ok a stored entry is converted to its typed form when the credential file is read (cold)
	if err := json.Unmarshal(encoded, &credential); err != nil { //portlint:allow jsonclone a typed value converted from its stored JSON form, cold (see the perf:ok above)
		return nil, err
	}
	return &credential, nil
}

// fileFingerprint is `ino:mtimeNs:ctimeNs:size` of path, "missing" when it does not exist.
func fileFingerprint(path string) (string, error) {
	var info syscall.Stat_t
	if err := syscall.Stat(path, &info); err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return "missing", nil
		}
		return "", err
	}
	mtime, ctime := statTimes(&info)
	return fmt.Sprintf("%d:%d:%d:%d", info.Ino, mtime, ctime, info.Size), nil
}

// FileCredentials is the credential store of auth.json: every operation under the file's lock, reading the file again;
// the last valid content stands in while the file is invalid (never at startup: invalid startup auth fails closed).
type FileCredentials struct {
	file *p2runtime.LockedFile
	path string

	mu       sync.Mutex
	lastGood *chordjson.Object
	revision string
}

var _ ai.CredentialStore = (*FileCredentials)(nil)

// NewFileCredentials is the credential store of the file at path.
func NewFileCredentials(path string) *FileCredentials {
	normalized := p2runtime.NormalizePath(path)
	return &FileCredentials{file: p2runtime.NewLockedFile(normalized), path: normalized}
}

// Revision is the file's fingerprint as of the last reload; empty before one.
func (store *FileCredentials) Revision() string {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.revision
}

// Close refuses later operations and waits for the running ones.
func (store *FileCredentials) Close() { store.file.Close() }

// Reload reads the file under its lock and keeps it as the last valid content.
func (store *FileCredentials) Reload(ctx context.Context) error {
	type snapshot struct {
		data     *chordjson.Object
		revision string
	}
	loaded, err := p2runtime.WithLock(ctx, store.file, func(content *string) (p2runtime.LockResult[snapshot], error) {
		data, err := parseCredentials(content)
		if err != nil {
			return p2runtime.LockResult[snapshot]{}, err
		}
		revision, err := fileFingerprint(store.path)
		if err != nil {
			return p2runtime.LockResult[snapshot]{}, err
		}
		return p2runtime.LockResult[snapshot]{Result: snapshot{data, revision}}, nil
	})
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.lastGood, store.revision = loaded.data, loaded.revision
	store.mu.Unlock()
	return nil
}

func (store *FileCredentials) latest(ctx context.Context) (*chordjson.Object, error) {
	if err := store.Reload(ctx); err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}
		store.mu.Lock()
		empty := store.lastGood == nil
		store.mu.Unlock()
		if empty {
			return nil, err // Fail closed on invalid startup auth.
		}
	}
	if cause := context.Cause(ctx); cause != nil {
		return nil, cause
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.lastGood == nil {
		return chordjson.NewObject(0), nil
	}
	return store.lastGood, nil
}

// StoredSnapshot is the stored credential as last read (a copy; its key unresolved), nil when there is none.
func (store *FileCredentials) StoredSnapshot(providerID string) *ai.Credential {
	store.mu.Lock()
	data := store.lastGood
	store.mu.Unlock()
	if data == nil {
		return nil
	}
	entry, ok := data.Get(providerID)
	if !ok {
		return nil
	}
	credential, err := credentialOf(entry)
	if err != nil {
		return nil
	}
	return credential
}

// Metadata is what is stored as last read, without the secrets, in the file's key order.
func (store *FileCredentials) Metadata() []ai.CredentialInfo {
	store.mu.Lock()
	data := store.lastGood
	store.mu.Unlock()
	if data == nil {
		return nil
	}
	out := make([]ai.CredentialInfo, 0, data.Len())
	for providerID, entry := range data.All() {
		kind, _ := entry.(*chordjson.Object).Value("type").(string)
		out = append(out, ai.CredentialInfo{ProviderID: providerID, Type: ai.CredentialType(kind)})
	}
	return out
}

// Read is the stored credential; an api_key one has its key resolved as a configuration value (an error when it cannot
// be).
func (store *FileCredentials) Read(ctx context.Context, providerID string) (*ai.Credential, error) {
	data, err := store.latest(ctx)
	if err != nil {
		return nil, err
	}
	entry, ok := data.Get(providerID)
	if !ok {
		return nil, nil
	}
	credential, err := credentialOf(entry)
	if err != nil {
		return nil, err
	}
	if credential.Type != ai.CredentialAPIKey || !entry.(*chordjson.Object).Has("key") {
		return credential, nil
	}
	key, resolved := ResolveConfigValueUncached(credential.Key, credential.Env)
	if !resolved {
		return nil, errors.New("Could not resolve stored API key")
	}
	credential.Key = key
	return credential, nil
}

// List is what is stored, without resolving anything or exposing key, env or token values.
func (store *FileCredentials) List(ctx context.Context) ([]ai.CredentialInfo, error) {
	if _, err := store.latest(ctx); err != nil {
		return nil, err
	}
	return store.Metadata(), nil
}

// Modify runs update with a copy of the current credential under the file's lock; nil leaves the entry as it is. A
// cancelled ctx stops waiting, not ownership: the operation keeps its lock until it settles, and cannot write after the
// cancellation.
func (store *FileCredentials) Modify(ctx context.Context, providerID string, update func(current *ai.Credential) (*ai.Credential, error)) (*ai.Credential, error) {
	type outcome struct {
		credential *ai.Credential
		err        error
	}
	done := make(chan outcome, 1)
	go func() {
		type modified struct {
			data       *chordjson.Object
			credential any
		}
		result, err := p2runtime.WithLock(ctx, store.file, func(content *string) (p2runtime.LockResult[modified], error) {
			data, err := parseCredentials(content)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			var current *ai.Credential
			if entry, ok := data.Get(providerID); ok {
				if current, err = credentialOf(entry); err != nil {
					return p2runtime.LockResult[modified]{}, err
				}
			}
			candidate, err := update(current)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			if candidate == nil {
				entry, _ := data.Get(providerID)
				return p2runtime.LockResult[modified]{Result: modified{data, entry}}, nil
			}
			encoded, err := json.Marshal(candidate)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			//perf:ok a credential file write (cold)
			value, err := chordjson.Decode(encoded)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			spread := data.Clone()
			spread.Set(providerID, value)
			next, err := parseCredentialsValue(spread)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			text, err := p2runtime.StringifyIndented(next)
			if err != nil {
				return p2runtime.LockResult[modified]{}, err
			}
			entry, _ := next.Get(providerID)
			return p2runtime.LockResult[modified]{Result: modified{next, entry}, Next: &text}, nil
		})
		if err != nil {
			done <- outcome{err: err}
			return
		}
		store.mu.Lock()
		store.lastGood = result.data
		store.mu.Unlock()
		if result.credential == nil {
			done <- outcome{}
			return
		}
		credential, err := credentialOf(result.credential)
		done <- outcome{credential, err}
	}()
	select {
	case result := <-done:
		return result.credential, result.err
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
}

// Delete removes the stored credential (nothing happens when there is none).
func (store *FileCredentials) Delete(ctx context.Context, providerID string) error {
	data, err := p2runtime.WithLock(ctx, store.file, func(content *string) (p2runtime.LockResult[*chordjson.Object], error) {
		next, err := parseCredentials(content)
		if err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		next.Delete(providerID)
		text, err := p2runtime.StringifyIndented(next)
		if err != nil {
			return p2runtime.LockResult[*chordjson.Object]{}, err
		}
		return p2runtime.LockResult[*chordjson.Object]{Result: next, Next: &text}, nil
	})
	if err != nil {
		return err
	}
	store.mu.Lock()
	store.lastGood = data
	store.mu.Unlock()
	return nil
}
