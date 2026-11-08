package mcp

import (
	"encoding/json"
	"fmt"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/internal/chord/chordjson"
	pimcp "github.com/MichaelKinsy/PiG/mcp"
)

// The conversions between the plugin's Go values and the JSON its state members and documents carry (Pi's values are the
// JSON objects themselves).

// toJSON is value as the JSON a state member carries.
func toJSON(value any) delta.JsonValue {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("p2 mcp: %v", err))
	}
	decoded, err := chordjson.Decode(encoded)
	if err != nil {
		panic(fmt.Sprintf("p2 mcp: %v", err))
	}
	return decoded
}

// fromJSON is the T a state member's or a document's JSON holds.
func fromJSON[T any](value delta.JsonValue) (T, error) {
	var out T
	encoded, err := chordjson.Marshal(value)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(encoded, &out)
	return out, err
}

// locationForm is a Location as the contracts carry it.
type locationForm struct {
	Cwd string  `json:"cwd"`
	Env *string `json:"env"`
}

func locationOf(form locationForm) p2runtime.Location {
	return p2runtime.Location{Cwd: form.Cwd, Env: form.Env}
}

func formOf(location p2runtime.Location) locationForm {
	return locationForm{Cwd: location.Cwd, Env: location.Env}
}

// instanceForm is an `InstanceSchema` value.
type instanceForm struct {
	ID       string        `json:"id"`
	Server   string        `json:"server"`
	Source   string        `json:"source"`
	Location *locationForm `json:"location"`
	RunsOn   any           `json:"runsOn"`
	Auth     string        `json:"auth,omitempty"`
	Status   string        `json:"status"`
	Error    string        `json:"error,omitempty"`
	Tools    []pimcp.Tool  `json:"tools"`
}

func runsOnForm(runsOn RunOn) any {
	if runsOn.Kind == RunOnEnv {
		return map[string]any{"env": runsOn.Env}
	}
	return "host"
}

// instanceJSON is an instance as the manager lists it.
func instanceJSON(instance Instance) delta.JsonValue { return toJSON(instanceFormOf(instance, true)) }

func instanceFormOf(instance Instance, withError bool) instanceForm {
	form := instanceForm{
		ID: instance.ID, Server: instance.Server, Source: instance.Source, RunsOn: runsOnForm(instance.RunsOn), Status: instance.Status,
		Tools: instance.Tools,
	}
	if form.Tools == nil {
		form.Tools = []pimcp.Tool{}
	}
	if instance.Location != nil {
		location := formOf(*instance.Location)
		form.Location = &location
	}
	if instance.OAuth {
		form.Auth = "oauth"
	}
	if withError {
		form.Error = instance.Error
	}
	return form
}

// instancesOf is the instances a state member holds.
func instancesOf(value delta.JsonValue) ([]Instance, error) {
	forms, err := fromJSON[[]instanceForm](value)
	if err != nil {
		return nil, err
	}
	instances := make([]Instance, len(forms))
	for i, form := range forms {
		instances[i] = Instance{ID: form.ID, Server: form.Server, Source: form.Source, OAuth: form.Auth == "oauth", Status: form.Status, Error: form.Error, Tools: form.Tools}
		if form.Location != nil {
			location := locationOf(*form.Location)
			instances[i].Location = &location
		}
		if env, ok := form.RunsOn.(map[string]any); ok {
			name, _ := env["env"].(string)
			instances[i].RunsOn = RunOn{Kind: RunOnEnv, Env: name}
		} else {
			instances[i].RunsOn = RunOn{Kind: RunOnHost}
		}
	}
	return instances, nil
}

// leaseForm is a Lease as the leases document carries it.
type leaseForm struct {
	Session     string       `json:"session"`
	Location    locationForm `json:"location"`
	Project     projectForm  `json:"project"`
	Generation  int          `json:"generation"`
	Incarnation string       `json:"incarnation"`
}

type projectForm struct {
	Kind  string  `json:"kind"`
	Text  *string `json:"text,omitempty"`
	Error *string `json:"error,omitempty"`
}

func leasesJSON(leases []Lease) []any {
	out := make([]any, len(leases))
	for i, lease := range leases {
		form := leaseForm{Session: lease.Session, Location: formOf(lease.Location), Generation: lease.Generation, Incarnation: lease.Incarnation,
			Project: projectForm{Kind: string(lease.Project.Kind)}}
		switch lease.Project.Kind {
		case ProjectFile:
			form.Project.Text = &lease.Project.Text
		case ProjectUnreadable:
			form.Project.Error = &lease.Project.Error
		}
		out[i] = toJSON(form)
	}
	return out
}

func leasesOf(value delta.JsonValue) ([]Lease, error) {
	forms, err := fromJSON[[]leaseForm](value)
	if err != nil {
		return nil, err
	}
	leases := make([]Lease, len(forms))
	for i, form := range forms {
		project := ProjectSource{Kind: ProjectSourceKind(form.Project.Kind)}
		if form.Project.Text != nil {
			project.Text = *form.Project.Text
		}
		if form.Project.Error != nil {
			project.Error = *form.Project.Error
		}
		leases[i] = Lease{Session: form.Session, Location: locationOf(form.Location), Project: project, Generation: form.Generation, Incarnation: form.Incarnation}
	}
	return leases, nil
}

// ownerStateForm is an OwnerConfigState as the manager's followed state carries it.
type ownerStateForm struct {
	Servers []OwnerServer `json:"servers"`
	Errors  []string      `json:"errors"`
	Failure *string       `json:"failure"`
}

func ownerStateJSON(state OwnerConfigState) delta.JsonValue {
	servers := state.Servers
	if servers == nil {
		servers = []OwnerServer{}
	}
	errors := state.Errors
	if errors == nil {
		errors = []string{}
	}
	return toJSON(ownerStateForm{Servers: servers, Errors: errors, Failure: state.Failure})
}

func ownerStateOf(value delta.JsonValue) (OwnerConfigState, error) {
	form, err := fromJSON[ownerStateForm](value)
	return OwnerConfigState{OwnerConfig: OwnerConfig{Servers: form.Servers, Errors: form.Errors}, Failure: form.Failure}, err
}

func signInsJSON(views []SignInView) []any {
	out := make([]any, len(views))
	for i, view := range views {
		form := map[string]any{"id": view.ID, "server": view.Server, "via": view.Via, "status": view.Status}
		if view.Error != "" {
			form["error"] = view.Error
		}
		out[i] = toJSON(form)
	}
	return out
}

func instancesJSON(instances []Instance) []any {
	out := make([]any, len(instances))
	for i, instance := range instances {
		out[i] = instanceJSON(instance)
	}
	return out
}

func stringsJSON(values []string) []any {
	out := make([]any, len(values))
	for i, value := range values {
		out[i] = value
	}
	return out
}

// workOf is the environments each session's conversations work on, as the catalog and the subagent store record them: the
// session's own, then its subagents' own locations' (archived ones too); "" is this machine.
func workOf(sessions, children delta.JsonValue) (WorkEnvironments, delta.JsonValue) {
	work := WorkEnvironments{}
	shown := delta.NewJsonObject(0)
	byChildren, _ := children.(*delta.JsonObject)
	list, _ := sessions.([]any)
	for _, each := range list {
		session, _ := each.(*delta.JsonObject)
		id, _ := session.Value("id").(string)
		env, _ := session.Value("env").(string)
		environments, names := []string{env}, []any{nilIfEmpty(env)}
		if byChildren != nil {
			kids, _ := byChildren.Value(id).([]any)
			for _, kid := range kids {
				child, _ := kid.(*delta.JsonObject)
				location, _ := child.Value("location").(*delta.JsonObject)
				if location == nil {
					continue
				}
				if name, ok := location.Value("env").(string); ok && !containsString(environments, name) {
					environments, names = append(environments, name), append(names, name)
				}
			}
		}
		work[id] = environments
		shown.Set(id, names)
	}
	return work, shown
}

func nilIfEmpty(text string) any {
	if text == "" {
		return nil
	}
	return text
}

func containsString(list []string, item string) bool {
	for _, each := range list {
		if each == item {
			return true
		}
	}
	return false
}
