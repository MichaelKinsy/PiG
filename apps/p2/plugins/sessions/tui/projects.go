package tui

// Projects in the TUI (`projects.json`, the projects plugin): whether each place of a project is reachable, as the session selector's project rows show it, and
// resolving a place for `/move <env>`. A place is resolved with `Envs.listDirectory`, which expands `~` on its machine and fails when that machine is unreachable.
//
// Ports apps/p2/plugins/sessions/tui-projects.ts.

import (
	"context"
	"errors"
	"regexp"
	"sync"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/projects/contract"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
)

// PlaceKind is the state of a probed place.
type PlaceKind string

// The kinds: still checking; reachable at its absolute path; its machine answered but has no such directory (`missing`); or its machine could not be reached or is not
// configured (`unreachable`).
const (
	PlaceChecking    PlaceKind = "checking"
	PlaceReachable   PlaceKind = "reachable"
	PlaceMissing     PlaceKind = "missing"
	PlaceUnreachable PlaceKind = "unreachable"
)

// PlaceState is a probed place. Path is set when reachable, Reason (short, for people) when missing or unreachable.
type PlaceState struct {
	Kind         PlaceKind
	Path, Reason string
}

// PlaceMark is how a probed place reads in a project row: `?` is a machine that answered without the folder.
var PlaceMark = map[PlaceKind]struct{ Text, Color string }{
	PlaceChecking:    {"…", "muted"},
	PlaceReachable:   {"✓", "success"},
	PlaceMissing:     {"?", "warning"},
	PlaceUnreachable: {"✗", "error"},
}

// PlaceProblem is why a place cannot be used (`mac: no folder ~/x`); "" while it is checking or when it is reachable.
func PlaceProblem(label string, state PlaceState) string {
	if state.Kind == PlaceMissing || state.Kind == PlaceUnreachable {
		return label + ": " + state.Reason
	}
	return ""
}

// ResolvePlace is the absolute path of place on its machine; it blocks and fails when the machine or the directory cannot be reached.
func ResolvePlace(ctx runtime.TuiContext, place contract.ProjectPlace) (string, error) {
	envs, err := OwnerEnvs(ctx)
	if err != nil {
		return "", err
	}
	if variant, _, _ := envs.Variant(); variant != "owner" {
		return "", errors.New("Environments are owner-only")
	}
	pairs := []any{"path", place.Path}
	if place.Env != nil && *place.Env != "" {
		pairs = append(pairs, "env", *place.Env)
	}
	listing, err := envs.Call("listDirectory", delta.JsonObjectOf(pairs...), context.Background())
	if err != nil {
		return "", err
	}
	return p2tui.JStr(p2tui.JField(listing, "path")), nil
}

var (
	missingPattern       = regexp.MustCompile(`\b(ENOENT|ENOTDIR)\b|^Working directory is not a directory$`)
	notConfiguredPattern = regexp.MustCompile(`^Environment ".*" is not configured$`)
)

// PlaceFailure is why `Envs.listDirectory` failed for place, from its message (the only part that crosses the link): Node's and the pi-env daemon's
// `ENOENT`/`ENOTDIR` (`scandir`/`opendir`) and the SSH manager's "Working directory is not a directory" mean the machine answered without the folder;
// "Environment "x" is not configured" names no environment; anything else (refused, timed out, no daemon, …) is unreachable with its own message.
func PlaceFailure(place contract.ProjectPlace, err error) PlaceState {
	message := err.Error()
	switch {
	case missingPattern.MatchString(message):
		return PlaceState{Kind: PlaceMissing, Reason: "no folder " + place.Path}
	case notConfiguredPattern.MatchString(message):
		return PlaceState{Kind: PlaceUnreachable, Reason: "no such environment"}
	}
	return PlaceState{Kind: PlaceUnreachable, Reason: message}
}

func placeKey(place contract.ProjectPlace) string {
	env := ""
	if place.Env != nil {
		env = *place.Env
	}
	return env + "\x00" + place.Path
}

type probe struct {
	state PlaceState
	done  chan struct{}
}

// PlaceProbes are the places' probes: one per place, started in the background, replaced as a whole by Restart.
type PlaceProbes struct {
	mu      sync.Mutex
	probes  map[string]*probe
	ctx     runtime.TuiContext
	changed func()
}

// NewPlaceProbes is the probes of ctx; changed runs on the owner loop when a probe finished.
func NewPlaceProbes(ctx runtime.TuiContext, changed func()) *PlaceProbes {
	return &PlaceProbes{probes: map[string]*probe{}, ctx: ctx, changed: changed}
}

// Restart forgets every result and probes places again (the selector was shown).
func (p *PlaceProbes) Restart(places []contract.ProjectPlace) {
	p.mu.Lock()
	p.probes = map[string]*probe{}
	p.mu.Unlock()
	p.Probe(places)
}

// Probe probes the places not probed yet (projects.json changed).
func (p *PlaceProbes) Probe(places []contract.ProjectPlace) {
	for _, place := range places {
		p.start(place)
	}
}

// State is what is known of place: checking until it is probed.
func (p *PlaceProbes) State(place contract.ProjectPlace) PlaceState {
	p.mu.Lock()
	defer p.mu.Unlock()
	if existing := p.probes[placeKey(place)]; existing != nil {
		select {
		case <-existing.done:
		default:
			return PlaceState{Kind: PlaceChecking}
		}
		return existing.state
	}
	return PlaceState{Kind: PlaceChecking}
}

// Result is the outcome of place's probe, started now unless it runs or ran; it blocks until the probe finished.
func (p *PlaceProbes) Result(place contract.ProjectPlace) PlaceState {
	existing := p.start(place)
	<-existing.done
	return existing.state
}

func (p *PlaceProbes) start(place contract.ProjectPlace) *probe {
	key := placeKey(place)
	p.mu.Lock()
	existing := p.probes[key]
	if existing == nil {
		existing = &probe{done: make(chan struct{})}
		p.probes[key] = existing
		go func() {
			path, err := ResolvePlace(p.ctx, place)
			if err != nil {
				existing.state = PlaceFailure(place, err)
			} else {
				existing.state = PlaceState{Kind: PlaceReachable, Path: path}
			}
			close(existing.done)
			p.mu.Lock()
			current := p.probes[key] == existing
			p.mu.Unlock()
			// A restart replaced this probe: its outcome is no longer shown.
			if current {
				p.ctx.Post(p.changed)
			}
		}()
	}
	p.mu.Unlock()
	return existing
}
