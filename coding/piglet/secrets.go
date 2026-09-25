package piglet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/MichaelKinsy/PiG/coding/secretresolver"
	"github.com/MichaelKinsy/PiG/internal/ownerfile"
)

const maxSecretFileBytes = 1024 * 1024

// ResolvedSecrets contains transient secret values for immediate consumers. It
// must never be serialized, logged, placed in records/sessions, or returned by
// inspection APIs.
type ResolvedSecrets struct {
	values map[string][]byte
}

// Clear overwrites transient values and drops references after immediate use.
func (r *ResolvedSecrets) Clear() {
	if r == nil {
		return
	}
	for name, value := range r.values {
		for i := range value {
			value[i] = 0
		}
		delete(r.values, name)
	}
}

// ResolveRequiredSecrets resolves every logical secret consumed by the agent
// environment. Unused declarations remain inspectable without requiring
// machine-local availability.
func ResolveRequiredSecrets(ctx context.Context, p *Piglet) (*ResolvedSecrets, error) {
	if p == nil {
		return &ResolvedSecrets{values: map[string][]byte{}}, nil
	}
	declarations := make(map[string]SecretDeclaration, len(p.Secrets))
	for _, declaration := range p.Secrets {
		declarations[declaration.Name] = declaration
	}
	required := map[string]bool{}
	if p.AgentEnv != nil {
		for _, binding := range p.AgentEnv.Secrets {
			required[binding.SecretRef] = true
		}
	}
	resolved := &ResolvedSecrets{values: make(map[string][]byte, len(required))}
	for _, name := range slices.Sorted(maps.Keys(required)) {
		if ActiveAgentEnvironmentIdentity() != "" {
			environmentName := resolvedSecretEnvironmentName(name)
			if inherited, exists := os.LookupEnv(environmentName); exists && inherited != "" {
				resolved.values[name] = []byte(inherited)
				_ = os.Unsetenv(environmentName)
				continue
			}
		}
		declaration, exists := declarations[name]
		if !exists {
			return nil, fmt.Errorf("required secret %q is not declared", name)
		}
		value, kind, err := resolveSecretDeclaration(ctx, declaration)
		if err != nil {
			return nil, fmt.Errorf("resolve secret %q from %s: %w", name, kind, err)
		}
		resolved.values[name] = value
	}
	return resolved, nil
}

func resolveSecretDeclaration(ctx context.Context, declaration SecretDeclaration) ([]byte, string, error) {
	switch {
	case declaration.From.Env != "":
		value, exists := os.LookupEnv(declaration.From.Env)
		if !exists || value == "" {
			return nil, "env", fmt.Errorf("environment variable %s is missing or empty", declaration.From.Env)
		}
		return []byte(value), "env", nil
	case declaration.From.File != "":
		path := declaration.From.File
		if strings.HasPrefix(path, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, "file", fmt.Errorf("resolve home directory: %w", err)
			}
			path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, "file", fmt.Errorf("secret file is unavailable")
		}
		ownerOnly, err := ownerfile.OwnerOnly(path, info)
		if err != nil || !ownerOnly || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxSecretFileBytes {
			return nil, "file", fmt.Errorf("secret file must be a non-empty owner-only regular file no larger than %d bytes", maxSecretFileBytes)
		}
		value, err := os.ReadFile(path)
		if err != nil {
			return nil, "file", fmt.Errorf("secret file could not be read")
		}
		value = []byte(strings.TrimSuffix(string(value), "\n"))
		if len(value) == 0 {
			return nil, "file", fmt.Errorf("secret file value is empty")
		}
		return value, "file", nil
	case declaration.From.Ref != "":
		value, err := secretresolver.Resolve(ctx, declaration.From.Ref)
		return value, "ref", err
	default:
		return nil, "unknown", fmt.Errorf("secret source is empty")
	}
}

func resolvedSecretEnvironmentName(name string) string {
	digest := sha256.Sum256([]byte(name))
	return "PIG_RESOLVED_SECRET_" + strings.ToUpper(hex.EncodeToString(digest[:8]))
}

// RuntimeEnvironment carries resolved logical values into a verified inner Pig
// process without exposing values on argv. Keys reveal no logical names.
func (r *ResolvedSecrets) RuntimeEnvironment() (map[string]string, error) {
	environment := make(map[string]string, len(r.values))
	for name, value := range r.values {
		if strings.ContainsRune(string(value), '\x00') || strings.ContainsAny(string(value), "\r\n") {
			return nil, fmt.Errorf("secret %q cannot be represented as an environment value", name)
		}
		environment[resolvedSecretEnvironmentName(name)] = string(value)
	}
	return environment, nil
}

// AgentEnvironment returns child environment variables for declared bindings.
// Values are copied so callers may zero their map without mutating the resolver.
func (r *ResolvedSecrets) AgentEnvironment(p *Piglet) (map[string]string, error) {
	environment := map[string]string{}
	if p == nil || p.AgentEnv == nil {
		return environment, nil
	}
	for _, binding := range p.AgentEnv.Secrets {
		value, exists := r.values[binding.SecretRef]
		if !exists {
			return nil, fmt.Errorf("agent environment secret %q was not resolved", binding.SecretRef)
		}
		if strings.ContainsRune(string(value), '\x00') || strings.ContainsAny(string(value), "\r\n") {
			return nil, fmt.Errorf("agent environment secret %q cannot be represented as an environment value", binding.SecretRef)
		}
		environment[binding.Target.Env] = string(value)
	}
	return environment, nil
}
