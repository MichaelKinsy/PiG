package envs

import (
	"context"
	"errors"
	"path/filepath"
	"slices"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// The read side of apps/p2/plugins/envs/secrets.ts: what a sandbox's launcher needs of its secrets
// (<data>/sandbox-secrets.json, owner-only). Saving and deleting secrets belong to the envs plugin.

// SecretMeta is a secret as the sandbox's configuration names it (no value): what the launcher needs besides the value.
type SecretMeta struct {
	Name     string
	Hosts    []string
	Version  string
	Aliases  []string
	GitHosts []string
}

// presetExtras are, per secret preset id, what helps tools in the VM pick a secret up (SECRET_PRESETS in
// secret-presets.ts, the fields the launcher gets).
var presetExtras = map[string]struct{ aliases, gitHosts []string }{
	"github": {aliases: []string{"GH_TOKEN"}, gitHosts: []string{"github.com"}},
}

// SecretsPath is the file of the sandboxes' secrets in directory.
func SecretsPath(directory string) string { return filepath.Join(directory, "sandbox-secrets.json") }

var errInvalidSecrets = errors.New("Invalid sandbox-secrets.json")

// SandboxSecrets is the configuration part (no values) and the values of sandbox's secrets, for its launcher; names are in
// localeCompare order.
func SandboxSecrets(_ context.Context, directory, sandbox string) (meta []SecretMeta, values *chordjson.Object, err error) {
	values = chordjson.NewObject(0)
	text, err := p2runtime.OptionalText(SecretsPath(directory))
	if err != nil {
		return nil, nil, err
	}
	if text == nil || jsstring.Trim(*text) == "" {
		return nil, values, nil
	}
	value, err := chordjson.Decode([]byte(*text))
	if err != nil {
		return nil, nil, errInvalidSecrets
	}
	file, ok := value.(*chordjson.Object)
	if !ok {
		return nil, nil, errInvalidSecrets
	}
	var secrets *chordjson.Object
	if own, present := file.Get(sandbox); present && own != nil {
		if secrets, ok = own.(*chordjson.Object); !ok {
			return nil, nil, errInvalidSecrets
		}
	}
	names := secrets.Keys()
	slices.SortStableFunc(names, p2runtime.LocaleCompare)
	for _, name := range names {
		secret, ok := secrets.Value(name).(*chordjson.Object)
		if !ok {
			return nil, nil, errInvalidSecrets
		}
		hosts, okHosts := stringList(secret.Value("hosts"))
		version, okVersion := secret.Value("version").(string)
		secretValue, okValue := secret.Value("value").(string)
		if !okHosts || !okVersion || !okValue {
			return nil, nil, errInvalidSecrets
		}
		preset, _ := secret.Value("preset").(string)
		extras := presetExtras[preset]
		meta = append(meta, SecretMeta{Name: name, Hosts: hosts, Version: version, Aliases: slices.Clone(extras.aliases), GitHosts: slices.Clone(extras.gitHosts)})
		values.Set(name, secretValue)
	}
	return meta, values, nil
}

func stringList(value any) ([]string, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	out := make([]string, len(items))
	for at, item := range items {
		if out[at], ok = item.(string); !ok {
			return nil, false
		}
	}
	return out, true
}
