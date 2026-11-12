// Ports packages/durable/src/harness/parks.ts: tool parks, their shape check, and how the Harness tells a base tool's
// parks from a wrapper's.

package harness

import (
	"context"
	"runtime"
	"sync"
	"weak"

	"github.com/MichaelKinsy/PiG/durable"
)

// isPark reports whether Execute returned a park rather than a result (parks.ts isPark). Upstream's park is its own
// object type; Go's result carries it in Park, which is a pointer so a wrapper's park can be told from its base's.
func isPark(result durable.ToolExecutionResult) bool { return result.Park != nil }

// baseParks is the set of parks a wrapped base returned in one run of a tool (parks.ts BASE_PARKS and ParkCarrier).
// Upstream carries it as an own enumerable symbol property of the run's api, which a wrapper that spreads the api passes
// on to the base. A Go wrapper hands its base the api it was given, or a struct that embeds it; the run carries the set on
// the api (baseParksOf finds it when the wrapper passes the api itself) and on the context its Execute and Cancel get
// (when the wrapper passes an api of its own and a context derived from that one).
type baseParks struct {
	mu    sync.Mutex
	parks map[*durable.ToolPark]struct{}
}

func (set *baseParks) add(park *durable.ToolPark) {
	set.mu.Lock()
	defer set.mu.Unlock()
	if set.parks == nil {
		set.parks = map[*durable.ToolPark]struct{}{}
	}
	set.parks[park] = struct{}{}
}

func (set *baseParks) has(park *durable.ToolPark) bool {
	set.mu.Lock()
	defer set.mu.Unlock()
	_, found := set.parks[park]
	return found
}

// parkCarrier is an api that carries its run's baseParks.
type parkCarrier interface{ carriedParks() *baseParks }

type baseParksKey struct{}

// withBaseParks returns ctx carrying the run's baseParks.
func withBaseParks(ctx context.Context, set *baseParks) context.Context {
	return context.WithValue(ctx, baseParksKey{}, set)
}

// baseParksOf is the baseParks of the run whose api, or context, a base got; nil when neither carries one.
func baseParksOf(ctx context.Context, api durable.ToolExecutionApi) *baseParks {
	if carrier, ok := api.(parkCarrier); ok {
		return carrier.carriedParks()
	}
	set, _ := ctx.Value(baseParksKey{}).(*baseParks)
	return set
}

// parkError is why a park breaks its shape (spec suspendable §4.1), or "": exactly one of Sleep and Wait, no result
// field, and, when tool is a wrapper's composite (§9), one its base returned in this run, whose parks are set. Rules on
// what it waits for are the commit's.
func parkError(tool *durable.ToolRegistration, result durable.ToolExecutionResult, set *baseParks) string {
	park := result.Park
	// Tool code is a trust boundary for the mixed objects its types forbid; Go's one struct holds a park beside a result.
	if park.Sleep != nil && park.Wait != nil {
		return "a park has either sleep or wait, not both"
	}
	if park.Sleep == nil && park.Wait == nil {
		return "a park has either sleep or wait, but this one has neither"
	}
	if field := resultField(result); field != "" {
		return "a park has no result field, but this one has " + field
	}
	if composites.has(tool) && (set == nil || !set.has(park)) {
		return "a wrapper returned a park its base did not return"
	}
	return ""
}

// resultField is the first result field a park carries, in parks.ts RESULT_FIELDS order; "" when none.
func resultField(result durable.ToolExecutionResult) string {
	switch {
	case result.Output != nil:
		return "output"
	case result.HasStructuredOutput:
		return "structuredOutput"
	case result.IsError != nil:
		return "isError"
	case result.HasDetails:
		return "details"
	case result.Diagnostics != nil:
		return "diagnostics"
	case result.Usage != nil:
		return "usage"
	case result.Control != nil:
		return "control"
	}
	return ""
}

// weakToolSet is a set of tool registrations by identity that does not keep them alive (a WeakSet).
type weakToolSet struct{ members sync.Map }

func (set *weakToolSet) add(tool *durable.ToolRegistration) {
	key := weak.Make(tool)
	if _, loaded := set.members.LoadOrStore(key, struct{}{}); !loaded {
		runtime.AddCleanup(tool, func(key weak.Pointer[durable.ToolRegistration]) { set.members.Delete(key) }, key)
	}
}

func (set *weakToolSet) has(tool *durable.ToolRegistration) bool {
	_, found := set.members.Load(weak.Make(tool))
	return found
}

// composites are the tools a wrapper built; their parks must come from their base.
var composites weakToolSet

// wrapComposite applies wrap to tool as tool wrappers apply (spec suspendable §9): the first wrapper of a base gets a copy
// whose Execute records every park it returns in its run's baseParks, so the composite's own parks are told apart.
func wrapComposite(tool *durable.ToolRegistration, wrap func(*durable.ToolRegistration) *durable.ToolRegistration) *durable.ToolRegistration {
	base := tool
	if !composites.has(tool) {
		recording := *tool
		if execute := tool.Execute; execute != nil {
			recording.Execute = func(ctx context.Context, args any, api durable.ToolExecutionApi) (durable.ToolExecutionResult, error) {
				result, err := execute(ctx, args, api)
				if err == nil && isPark(result) {
					if set := baseParksOf(ctx, api); set != nil {
						set.add(result.Park)
					}
				}
				return result, err
			}
		}
		base = &recording
	}
	composite := wrap(base)
	if composite != nil {
		composites.add(composite)
	}
	return composite
}
