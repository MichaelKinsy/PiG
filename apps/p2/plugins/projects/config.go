// Package projects ports apps/p2/plugins/projects (server and contract parts): `projects.json`, which sessions belong
// to which project, and the owner's `Projects`.
package projects

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	"github.com/MichaelKinsy/PiG/schema"
)

// Ports apps/p2/plugins/projects/config.ts: `projects.json` in the data directory: `{ "<name>": [{ "env"?: "<environment>",
// "path": "<directory>" }, ...] }`. Object order is display order, array order is preference; a place without `env` is
// this device.

// envName is envs' `ENV_NAME`: the names an environment may have (copied: plugins share only contracts).
var envName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

var isPlace = func() *schema.Validator {
	compiled, err := schema.Compile(schema.Obj(schema.Properties{
		schema.Opt("env", schema.Str(schema.MinLength(1), schema.MaxLength(32))),
		schema.Prop("path", schema.Str(schema.MinLength(1), schema.MaxLength(4096))),
	}), schema.CompileOptions{})
	if err != nil {
		panic(err)
	}
	return compiled
}()

// hasControl: whether value has a control character (C0 or DEL), as envs' configuration checks.
func hasControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

// ParseProjects reads `projects.json`'s text; it fails with the first problem, naming the project.
func ParseProjects(content string) ([]contract.Project, error) {
	value, err := chordjson.Decode([]byte(p2runtime.StripJSONComments(p2runtime.StripBOM(content))))
	if err != nil {
		return nil, errors.New("Invalid projects.json syntax")
	}
	object, ok := value.(*chordjson.Object)
	if !ok {
		return nil, errors.New("projects.json must be an object of projects")
	}
	projects := make([]contract.Project, 0, object.Len())
	for name, places := range object.All() {
		parsed, err := parsePlaces(name, places)
		if err != nil {
			return nil, err
		}
		projects = append(projects, contract.Project{Name: name, Places: parsed})
	}
	return projects, nil
}

// parsePlaces is one project's places: absolute or `~` paths, at most one per environment.
func parsePlaces(name string, places any) ([]contract.ProjectPlace, error) {
	if p2runtime.JSTrim(name) == "" || p2runtime.JSLength(name) > 64 || hasControl(name) {
		return nil, fmt.Errorf("Invalid project name: %s", name)
	}
	list, ok := places.([]any)
	if !ok || len(list) == 0 {
		return nil, fmt.Errorf(`Project %s: a list of places ({ "env"?, "path" }) is required`, name)
	}
	seen := map[string]bool{}
	parsed := make([]contract.ProjectPlace, 0, len(list))
	for _, each := range list {
		if ok, _ := isPlace.Check(each); !ok {
			return nil, fmt.Errorf(`Project %s: a place is { "env"?: string, "path": string }`, name)
		}
		place := each.(*chordjson.Object)
		path, _ := place.Value("path").(string)
		var env *string
		key := ""
		if value, present := place.Value("env").(string); present {
			if !envName.MatchString(value) {
				return nil, fmt.Errorf("Project %s: invalid environment name %s", name, value)
			}
			env, key = &value, "e:"+value
		}
		if !p2runtime.IsRooted(path) || hasControl(path) {
			return nil, fmt.Errorf("Project %s: the path must be absolute or start with ~ (%s)", name, path)
		}
		if seen[key] {
			on := "this device"
			if env != nil {
				on = *env
			}
			return nil, fmt.Errorf("Project %s: two places on %s", name, on)
		}
		seen[key] = true
		parsed = append(parsed, contract.ProjectPlace{Env: env, Path: path})
	}
	return parsed, nil
}

// ProjectsPath is projects.json in directory.
func ProjectsPath(directory string) string { return filepath.Join(directory, "projects.json") }

// LoadProjects is the projects of directory's `projects.json` (none without one).
func LoadProjects(_ context.Context, directory string) ([]contract.Project, error) {
	content, err := p2runtime.OptionalText(ProjectsPath(directory))
	if err != nil {
		return nil, err
	}
	if content == nil {
		return ParseProjects("{}")
	}
	return ParseProjects(*content)
}
