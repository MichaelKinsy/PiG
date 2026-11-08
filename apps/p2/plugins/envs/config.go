// Package envs is p2's environments plugin. Ported so far: envs.json (config.ts, store.ts) and the server's `EnvService`
// with the manager's local part (`EnvManagers`: working directories on this machine). The SSH manager, sandboxes, secrets,
// the `Envs` page contract and the restart contribution are recorded gaps (rows/p2-A.md).
package envs

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/envs/config.ts (and the schema.ts name check): envs.json, validated; an invalid save keeps the last
// valid configuration.

var (
	envName       = regexp.MustCompile(contract.EnvNamePattern)
	imagePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/@-]*$`)
	hostPattern   = regexp.MustCompile(`^[A-Za-z0-9*][A-Za-z0-9.*-]*$`)
	wasWritableAt = regexp.MustCompile(`^~/\.pi(/|$)`)
	validator     = func() *schema.Validator {
		compiled, err := schema.Compile(contract.EnvConfigSchema, schema.CompileOptions{})
		if err != nil {
			panic(err)
		}
		return compiled
	}()
	whitespace = regexp.MustCompile(`\s`)
)

// EnvsConfig is envs.json: the environments by name, each its validated JSON, in the file's order.
type EnvsConfig = *chordjson.Object

func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

// sshField: no leading `-` (it would be read as an `ssh` option) and no white space or control characters.
func sshField(value string, present bool) bool {
	return !present || (!strings.HasPrefix(value, "-") && !whitespaceIn(value) && !hasControl(value))
}

func whitespaceIn(value string) bool {
	return whitespace.MatchString(value) || strings.ContainsFunc(value, unicode.IsSpace)
}

// reserved are the paths p2 uses in a sandbox: where pi-env's daemon is copied, where Gondolin mounts the folders.
var reserved = []string{"/opt/pi-env", "/.p2-vfs"}

// guestPath is a path inside a sandbox: absolute, normalized, not the root, without control characters.
func guestPath(path string) bool {
	if !strings.HasPrefix(path, "/") || path == "/" || hasControl(path) {
		return false
	}
	for _, part := range strings.Split(path, "/")[1:] {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func text(object *chordjson.Object, key string) (string, bool) {
	value, ok := object.Value(key).(string)
	return value, ok
}

// ValidateEnv is one configuration on its own, copied (a sandbox's host is checked against the others by ParseEnvs).
func ValidateEnv(value any) (*chordjson.Object, error) {
	if ok, _ := validator.Check(value); !ok {
		return nil, errors.New("Invalid environment configuration")
	}
	copied, err := chordjson.Copy(value)
	if err != nil {
		return nil, err
	}
	config := copied.(*chordjson.Object)
	if config.Value("type") == "gondolin" {
		host, _ := text(config, "host")
		if !envName.MatchString(host) {
			return nil, errors.New("Invalid host environment")
		}
		if image, ok := text(config, "image"); ok && !imagePattern.MatchString(image) {
			return nil, errors.New("Invalid image")
		}
		guests := map[string]bool{}
		mounts, _ := config.Value("mounts").([]any)
		for _, each := range mounts {
			mount := each.(*chordjson.Object)
			mountHost, _ := text(mount, "host")
			guest, _ := text(mount, "guest")
			hostPath := mountHost == "~" || strings.HasPrefix(mountHost, "~/") || strings.HasPrefix(mountHost, "/")
			if !hostPath || hasControl(mountHost) {
				return nil, fmt.Errorf("Invalid folder: %s", mountHost)
			}
			if !guestPath(guest) {
				return nil, fmt.Errorf("Invalid path in the sandbox: %s", guest)
			}
			for _, path := range reserved {
				if guest == path || strings.HasPrefix(path, guest+"/") || strings.HasPrefix(guest, path+"/") {
					return nil, fmt.Errorf("%s is used by p2 in the sandbox (%s)", guest, strings.Join(reserved, ", "))
				}
			}
			// The launcher refuses these too, after resolving the path on the host; this is the form's early answer.
			if readonly, _ := mount.Value("readonly").(bool); !readonly && (mountHost == "~" || mountHost == "/" || wasWritableAt.MatchString(mountHost)) {
				return nil, fmt.Errorf("A writable folder must not contain or be inside ~/.pi: %s", mountHost)
			}
			if guests[guest] {
				return nil, fmt.Errorf("Two folders at %s", guest)
			}
			guests[guest] = true
		}
		allowed, _ := config.Value("allowHosts").([]any)
		for _, each := range allowed {
			if pattern := each.(string); !hostPattern.MatchString(pattern) {
				return nil, fmt.Errorf("Invalid allowed host: %s", pattern)
			}
		}
		return config, nil
	}
	host, hasHost := text(config, "host")
	user, hasUser := text(config, "user")
	if !sshField(host, hasHost) || !sshField(user, hasUser) {
		return nil, errors.New("Invalid host or user")
	}
	identity, _ := text(config, "identityFile")
	if hasControl(identity) || strings.Contains(identity, `"`) {
		return nil, errors.New("Invalid identity file")
	}
	return config, nil
}

// checkHosts: every sandbox runs on a configured SSH environment.
func checkHosts(envs EnvsConfig) error {
	for name, each := range envs.All() {
		config := each.(*chordjson.Object)
		if config.Value("type") != "gondolin" {
			continue
		}
		host, _ := text(config, "host")
		if on, ok := envs.Value(host).(*chordjson.Object); !ok || on.Value("type") != "ssh" {
			return fmt.Errorf(`Sandbox %s runs on "%s", which is not an SSH environment`, name, host)
		}
	}
	return nil
}

// ParseEnvs is the configuration in content (comments and a byte order mark allowed).
func ParseEnvs(content string) (EnvsConfig, error) {
	value, err := chordjson.Decode([]byte(p2runtime.StripJSONComments(p2runtime.StripBOM(content))))
	if err != nil {
		return nil, errors.New("Invalid envs.json syntax")
	}
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil, errors.New("Invalid envs.json configuration")
	}
	envs := chordjson.NewObject(object.Len())
	for name, config := range object.All() {
		if !envName.MatchString(name) {
			return nil, fmt.Errorf("Invalid environment name: %s", name)
		}
		validated, err := ValidateEnv(config)
		if err != nil {
			return nil, err
		}
		envs.Set(name, validated)
	}
	return envs, checkHosts(envs)
}

// EnvsPath is envs.json in directory.
func EnvsPath(directory string) string { return filepath.Join(directory, "envs.json") }

// LoadEnvs reads envs.json (absent: no environments).
func LoadEnvs(_ context.Context, directory string) (EnvsConfig, error) {
	content, err := p2runtime.OptionalText(EnvsPath(directory))
	if err != nil {
		return nil, err
	}
	if content == nil {
		return ParseEnvs("{}")
	}
	return ParseEnvs(*content)
}
