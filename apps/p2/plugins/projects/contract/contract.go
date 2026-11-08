// Package contract ports apps/p2/plugins/projects/contract.ts: one codebase's places on several machines
// (`projects.json`), which sessions belong to which project, and the helpers that find a project's place on a machine
// and a directory's subpath below a place (a leaf package other plugins import).
package contract

import (
	"strings"

	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	"github.com/MichaelKinsy/PiG/chord/next/contracts"
	"github.com/MichaelKinsy/PiG/schema"
)

func must(contract *contracts.Contract, err error) *contracts.Contract {
	if err != nil {
		panic(err)
	}
	return contract
}

var c = p2runtime.Contract

// ProjectPlaceSchema is where a project lives on one machine: `env` null is this device; `path` may start with `~`
// (that machine's home).
var ProjectPlaceSchema = schema.Obj(schema.Properties{
	schema.Prop("env", schema.Union([]schema.Schema{schema.Str(), schema.Nul()})),
	schema.Prop("path", schema.Str()),
})

// ProjectPlace is a place of a project; Env nil is this device.
type ProjectPlace struct {
	Env  *string
	Path string
}

// ProjectSchema is a project: its name and its places, most preferred first, at most one per environment.
var ProjectSchema = schema.Obj(schema.Properties{
	schema.Prop("name", schema.Str()),
	schema.Prop("places", schema.Arr(ProjectPlaceSchema)),
})

// Project is a project (ProjectSchema).
type Project struct {
	Name   string
	Places []ProjectPlace
}

// assignSchema: session `session` belongs to project `project` from now on.
var assignSchema = schema.Obj(schema.Properties{
	schema.Prop("session", schema.Str(schema.MaxLength(64))),
	schema.Prop("project", schema.Str(schema.MaxLength(64))),
})

var nullable = func(value schema.Schema) schema.Schema { return schema.Union([]schema.Schema{value, schema.Nul()}) }

// ProjectService is `projects.json` as last read validly (in the file's order), why the last read failed, and the
// sessions' projects by session ID (`p2.project-service`; server, host-internal; persisted in the server's store, never
// in the session catalog). A session belongs to a project only when it was started from the project's row (`assign`) or
// copied from such a session (`copy`, the sessions plugin's `SessionCopied`); nothing clears it, so a name may outlive
// its project in projects.json.
var ProjectService = must(contracts.Local("p2.project-service", contracts.LocalDeclaration{
	Version: 1,
	Params:  contracts.Params{},
	Members: []contracts.LocalEntry{
		contracts.Mem("projects", c(schema.Arr(ProjectSchema))),
		contracts.Mem("failure", c(nullable(schema.Str()))),
		contracts.Mem("sessions", c(p2runtime.Record(schema.Str()))),
		// Refused for a project projects.json does not name.
		contracts.Mem("assign", contracts.NewMethod(c(assignSchema))),
		// `copy` belongs to `source`'s project, if it has one.
		contracts.Mem("copy", contracts.NewMethod(c(schema.Obj(schema.Properties{
			schema.Prop("source", schema.Str()), schema.Prop("copy", schema.Str()),
		})))),
	},
}))

// Projects is the owner's projects, the last read's failure, this device's name in place labels (the server's hostname)
// and the sessions' projects by session ID; `assign` is the TUI's project rows starting a session (`p2.projects`).
var Projects = must(contracts.Remote("p2.projects", contracts.RemoteDeclaration{
	Version:     1,
	Params:      contracts.Params{},
	HostAddress: p2runtime.ServerHost,
	Variants: []contracts.VariantEntry{
		contracts.Var("owner", contracts.VariantDeclaration{
			State: []contracts.Property{
				p2runtime.StateProp("projects", schema.Arr(ProjectSchema)),
				p2runtime.StateProp("failure", nullable(schema.Str())),
				p2runtime.StateProp("local", schema.Str()),
				p2runtime.StateProp("sessions", p2runtime.Record(schema.Str())),
			},
			Methods: []contracts.MethodEntry{contracts.Meth("assign", contracts.NewMethod(c(assignSchema)))},
		}),
	},
}))

// PlaceLabel is how a place's machine reads: its environment's name, or this device's label.
func PlaceLabel(place ProjectPlace, localLabel string) string {
	if place.Env != nil {
		return *place.Env
	}
	return localLabel
}

func trimLeadingSeparators(text string) string { return strings.TrimLeft(text, `\/`) }

func trimTrailingSeparators(text string) string { return strings.TrimRight(text, `\/`) }

// Below is where the absolute `path` sits below the place path `root`, from the text alone: the rest below it ("" at
// the root; either separator), with ok false outside it. An absolute root contains what isWithin says. `~/rel` contains
// `path` when `path` has `/rel` at segment boundaries after a non-empty prefix (that machine's home, unknown here). `~`
// contains every path, its rest unknown without the home: "".
func Below(path, root string) (rest string, ok bool) {
	if strings.HasPrefix(root, "~") {
		relative := strings.Trim(strings.ReplaceAll(root[1:], `\`, "/"), "/")
		if relative == "" {
			return "", true
		}
		// Separators made alike without changing lengths, so an index in `text` is one in `path`.
		text := strings.ReplaceAll(path, `\`, "/")
		needle := "/" + relative
		for from := 1; from <= len(text); {
			found := strings.Index(text[from:], needle)
			if found < 0 {
				break
			}
			index := from + found
			end := index + 1 + len(relative)
			if end == len(text) || text[end] == '/' {
				return trimLeadingSeparators(path[end:]), true
			}
			from = index + 1
		}
		return "", false
	}
	if !isWithin(path, root) {
		return "", false
	}
	return trimLeadingSeparators(path[len(trimTrailingSeparators(root)):]), true
}

// isWithin: whether `path` is `root` or below it (either separator, so Windows machines match too).
func isWithin(path, root string) bool {
	base := trimTrailingSeparators(root)
	return path == root || path == base || strings.HasPrefix(path, base+"/") || strings.HasPrefix(path, base+`\`)
}

// PlaceOn is the place of `project` on the machine `name` names: an environment's name, or this device as `local` or
// its label (case-insensitive, as host names are). An environment of that name wins over this device.
func PlaceOn(project Project, name, localLabel string) (ProjectPlace, bool) {
	for _, place := range project.Places {
		if place.Env != nil && *place.Env == name {
			return place, true
		}
	}
	if name == p2runtime.LocalEnv || strings.EqualFold(name, localLabel) {
		for _, place := range project.Places {
			if place.Env == nil {
				return place, true
			}
		}
	}
	return ProjectPlace{}, false
}
