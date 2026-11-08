package envs

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"slices"
	"strings"
	"time"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// Ports the owner side of apps/p2/plugins/envs/secrets.ts and secret-presets.ts: sandbox secrets
// (<data>/sandbox-secrets.json, 0600, owner-only), per sandbox, values its HTTP proxy injects into outbound request headers
// for their hosts. The VM only sees placeholders. The launcher reads the file where a sandbox is started (SandboxSecrets,
// values on stdin); the owner API lists names, hosts and age, never values.

// SecretName is a secret's environment variable name in the VM.
var SecretName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

// secretHost is a host pattern a secret may go to (as allowed hosts: `*` wildcards).
var secretHost = regexp.MustCompile(`^[A-Za-z0-9*][A-Za-z0-9.*-]*$`)

// SecretPreset is what helps tools in the VM pick a secret up: aliases (more variable names) and git's credential helper.
type SecretPreset struct {
	ID    string
	Label string
	Name  string
	Hosts []string
	// Aliases are further variables set to the same placeholder (`GH_TOKEN` for `gh`).
	Aliases []string
	// GitHosts are the hosts git authenticates to with this secret (a credential helper set through `GIT_CONFIG_*`).
	GitHosts []string
	Hint     string
}

// SecretPresets are the presets the Envs sheet offers.
var SecretPresets = []SecretPreset{
	{
		ID: "github", Label: "GitHub token", Name: "GITHUB_TOKEN",
		Hosts: []string{"github.com", "api.github.com", "uploads.github.com"}, Aliases: []string{"GH_TOKEN"}, GitHosts: []string{"github.com"},
		Hint: "git over HTTPS (a credential helper is set), gh (GH_TOKEN), the GitHub API. Use a fine-grained token.",
	},
	{
		ID: "radius", Label: "Radius token", Name: "RADIUS_TOKEN", Hosts: []string{"radius.earendil.com", "radius.pi.dev"},
		Hint: `Radius (artifacts at radius.earendil.com, the model gateway at radius.pi.dev): curl -H "Authorization: Bearer $RADIUS_TOKEN" …`,
	},
	{
		ID: "npm", Label: "npm token", Name: "NPM_TOKEN", Hosts: []string{"registry.npmjs.org"},
		Hint: "npm publish with //registry.npmjs.org/:_authToken=${NPM_TOKEN} in .npmrc (a granular token).",
	},
	{ID: "custom", Label: "API key", Name: "API_KEY", Hosts: []string{}, Hint: "Any HTTP header: Authorization, X-Api-Key, …"},
}

// SecretInfo is a secret as the owner sees it: everything but its value.
type SecretInfo struct {
	Name   string
	Hosts  []string
	Preset string
	SetAt  float64
}

// JSON is the secret as the owner API answers it.
func (info SecretInfo) JSON() *chordjson.Object {
	hosts := make([]any, len(info.Hosts))
	for at, host := range info.Hosts {
		hosts[at] = host
	}
	object := chordjson.ObjectOf("name", info.Name, "hosts", hosts)
	if info.Preset != "" {
		object.Set("preset", info.Preset)
	}
	object.Set("setAt", info.SetAt)
	return object
}

var errInvalidSecretsFile = errors.New("Invalid sandbox-secrets.json")

// readSecretsFile is the file's content: sandbox name to secret name to its stored object.
func readSecretsFile(text *string) (*chordjson.Object, error) {
	if text == nil || jsstring.Trim(*text) == "" {
		return chordjson.NewObject(0), nil
	}
	value, err := chordjson.Decode([]byte(*text))
	file, ok := value.(*chordjson.Object)
	if err != nil || !ok {
		return nil, errInvalidSecretsFile
	}
	return file, nil
}

func stringsOf(value any) []string {
	items, _ := value.([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

// ListSecrets are the secrets of sandbox, without values, in locale order.
func ListSecrets(directory, sandbox string) ([]SecretInfo, error) {
	text, err := p2runtime.OptionalText(SecretsPath(directory))
	if err != nil {
		return nil, err
	}
	file, err := readSecretsFile(text)
	if err != nil {
		return nil, err
	}
	own, _ := file.Value(sandbox).(*chordjson.Object)
	infos := []SecretInfo{}
	if own == nil {
		return infos, nil
	}
	names := own.Keys()
	slices.SortStableFunc(names, p2runtime.LocaleCompare)
	for _, name := range names {
		secret, _ := own.Value(name).(*chordjson.Object)
		if secret == nil {
			continue
		}
		preset, _ := secret.Value("preset").(string)
		setAt, _ := secret.Value("setAt").(float64)
		infos = append(infos, SecretInfo{Name: name, Hosts: stringsOf(secret.Value("hosts")), Preset: preset, SetAt: setAt})
	}
	return infos, nil
}

// AllSecretValues is every secret value of every sandbox (the log redactor's list).
func AllSecretValues(directory string) ([]string, error) {
	text, err := p2runtime.OptionalText(SecretsPath(directory))
	if err != nil {
		return nil, err
	}
	file, err := readSecretsFile(text)
	if err != nil {
		return nil, err
	}
	values := []string{}
	for _, each := range file.All() {
		secrets, _ := each.(*chordjson.Object)
		if secrets == nil {
			continue
		}
		for _, one := range secrets.All() {
			if secret, _ := one.(*chordjson.Object); secret != nil {
				if value, ok := secret.Value("value").(string); ok {
					values = append(values, value)
				}
			}
		}
	}
	return values, nil
}

// SecretChange is a save of one secret: without Value an existing secret keeps its value and gets the new hosts.
type SecretChange struct {
	Value  *string
	Hosts  []string
	Preset string
}

func newVersion() string {
	var bytes [6]byte
	for at := range bytes {
		bytes[at] = byte(rand.IntN(256))
	}
	return fmt.Sprintf("%x", bytes)
}

// SaveSecret sets (adds or replaces) secret name of sandbox. Every host must be one of the sandbox's allowed hosts
// (allowHosts): the proxy only sends to those.
func SaveSecret(ctx context.Context, directory, sandbox, name string, change SecretChange, allowHosts []string) error {
	if !SecretName.MatchString(name) {
		return errors.New("Secret names are environment variables: A-Z, 0-9 and _")
	}
	if len(change.Hosts) == 0 {
		return errors.New("A secret needs at least one host")
	}
	for _, host := range change.Hosts {
		if !secretHost.MatchString(host) {
			return fmt.Errorf("Invalid host: %s", host)
		}
		if !slices.Contains(allowHosts, host) {
			return fmt.Errorf("%s is not an allowed host of sandbox %s", host, sandbox)
		}
	}
	if change.Value != nil {
		if length := jsstring.Length(*change.Value); length == 0 || length > 16384 {
			return errors.New("A secret's value must have 1 to 16384 characters")
		}
		if strings.ContainsAny(*change.Value, "\r\n") {
			return errors.New("A secret's value is one line")
		}
	}
	return updateSecrets(ctx, directory, func(secrets *chordjson.Object) error {
		own, _ := secrets.Value(sandbox).(*chordjson.Object)
		if own == nil {
			own = chordjson.NewObject(1)
			secrets.Set(sandbox, own)
		}
		current, _ := own.Value(name).(*chordjson.Object)
		var value string
		if change.Value != nil {
			value = *change.Value
		} else if current != nil {
			value, _ = current.Value("value").(string)
		} else {
			return fmt.Errorf("Secret %s needs a value", name)
		}
		hosts := make([]any, len(change.Hosts))
		for at, host := range change.Hosts {
			hosts[at] = host
		}
		stored := chordjson.ObjectOf("hosts", hosts)
		preset := change.Preset
		if preset == "" && current != nil {
			preset, _ = current.Value("preset").(string)
		}
		if preset != "" {
			stored.Set("preset", preset)
		}
		setAt := float64(time.Now().UnixMilli())
		if change.Value == nil && current != nil {
			if previous, ok := current.Value("setAt").(float64); ok {
				setAt = previous
			}
		}
		stored.Set("value", value)
		stored.Set("version", newVersion())
		stored.Set("setAt", setAt)
		own.Set(name, stored)
		return nil
	})
}

// DeleteSecret removes secret name of sandbox (every secret of it when name is nil: the sandbox was deleted).
func DeleteSecret(ctx context.Context, directory, sandbox string, name *string) error {
	return updateSecrets(ctx, directory, func(secrets *chordjson.Object) error {
		if name == nil {
			secrets.Delete(sandbox)
			return nil
		}
		if own, _ := secrets.Value(sandbox).(*chordjson.Object); own != nil {
			own.Delete(*name)
			if own.Len() == 0 {
				secrets.Delete(sandbox)
			}
		}
		return nil
	})
}

func updateSecrets(ctx context.Context, directory string, change func(*chordjson.Object) error) error {
	file := p2runtime.NewLockedFile(SecretsPath(directory))
	defer file.Close()
	_, err := p2runtime.WithLock(ctx, file, func(current *string) (p2runtime.LockResult[struct{}], error) {
		secrets, err := readSecretsFile(current)
		if err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		if err := change(secrets); err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		text, err := p2runtime.StringifyIndented(secrets)
		if err != nil {
			return p2runtime.LockResult[struct{}]{}, err
		}
		text += "\n"
		return p2runtime.LockResult[struct{}]{Next: &text}, nil
	})
	return err
}
