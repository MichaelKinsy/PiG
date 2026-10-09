package chord

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type lifecycleOpt struct {
	provide, use                        *ServiceDefinition[Reader]
	setupThrow, activateThrow, ownThrow bool
}

func formatFacetError(err any) string {
	if aggregate, ok := err.(*AggregateError); ok {
		parts := make([]string, len(aggregate.Errors))
		for i, inner := range aggregate.Errors {
			parts[i] = formatFacetError(inner)
		}
		return aggregate.Message + "[" + strings.Join(parts, "; ") + "]"
	}
	return err.(error).Error()
}

// Pi packages/chord/src/facets/host.ts:388-421 (activate), 455-512 (reload), 514-519 (dispose), 750-790 (#terminate,
// #disposeLifecycles) and the FacetLifecycle disposal stack: the order in which a facet generation activates, retires
// and disposes its facets and owned disposals, and the errors it reports. Each expected error and log is what Pi's
// facet host reports for the same facets under Node.
func TestFacetLifecycleOrderMatchesPi(t *testing.T) {
	a, b := defineReader("lifeorder.a"), defineReader("lifeorder.b")
	impl := Reader(readerFunc(func(context.Context) (string, error) { return "", nil }))
	var log []string
	note := func(entry string, fail bool) error {
		log = append(log, entry)
		if fail {
			return errors.New(entry)
		}
		return nil
	}
	facet := func(id string, o lifecycleOpt) Facet {
		return DefineFacet(Facet{Id: id, Setup: func(env *FacetEnvironment) error {
			if o.provide != nil {
				if err := ProvideService(env, *o.provide, impl); err != nil {
					return err
				}
			}
			if o.use != nil {
				if _, err := UseService(env, *o.use); err != nil {
					return err
				}
			}
			steps := []error{
				env.Own(func(context.Context) error { return note("own1 "+id, o.ownThrow) }),
				env.OnActivate(func(context.Context) error { return note("activate "+id, o.activateThrow) }),
				env.OnDeactivate(func(context.Context) error { return note("onDeact "+id, false) }),
				env.Own(func(context.Context) error { return note("own2 "+id, false) }),
			}
			if err := errors.Join(steps...); err != nil {
				return err
			}
			if o.setupThrow {
				return errors.New("setup " + id)
			}
			return nil
		}})
	}
	for _, c := range []struct {
		name           string
		facets, reload []Facet
		want, log      string
	}{
		{"dispose order", []Facet{facet("c", lifecycleOpt{use: &b}), facet("b", lifecycleOpt{provide: &b, use: &a}), facet("a", lifecycleOpt{provide: &a})}, nil,
			"disposed", "activate a | activate b | activate c | own2 c | onDeact c | own1 c | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
		{"dispose errors", []Facet{facet("c", lifecycleOpt{use: &b}), facet("b", lifecycleOpt{provide: &b, use: &a, ownThrow: true}), facet("a", lifecycleOpt{provide: &a, ownThrow: true})}, nil,
			"dispose threw: Failed to dispose facet generation[own1 b; own1 a]", "activate a | activate b | activate c | own2 c | onDeact c | own1 c | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
		{"dispose one error", []Facet{facet("c", lifecycleOpt{use: &b}), facet("b", lifecycleOpt{provide: &b, use: &a, ownThrow: true}), facet("a", lifecycleOpt{provide: &a})}, nil,
			"dispose threw: own1 b", "activate a | activate b | activate c | own2 c | onDeact c | own1 c | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
		{"activate fail", []Facet{facet("c", lifecycleOpt{use: &b}), facet("b", lifecycleOpt{provide: &b, use: &a, activateThrow: true}), facet("a", lifecycleOpt{provide: &a, ownThrow: true})}, nil,
			"threw: Facet generation startup and cleanup failed[activate b; own1 a]", "activate a | activate b | own2 c | onDeact c | own1 c | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
		{"setup fail", []Facet{facet("x", lifecycleOpt{ownThrow: true}), facet("y", lifecycleOpt{setupThrow: true}), facet("z", lifecycleOpt{})}, nil,
			"threw: Facet generation startup and cleanup failed[setup y; own1 x]", "own2 y | onDeact y | own1 y | own2 x | onDeact x | own1 x"},
		{"reload activate fail", []Facet{facet("a", lifecycleOpt{provide: &a}), facet("b", lifecycleOpt{use: &a})}, []Facet{facet("b", lifecycleOpt{use: &a, activateThrow: true, ownThrow: true})},
			"reload threw: Facet reload activation and cleanup failed[activate b; own1 b] | disposed", "activate a | activate b | activate b | own2 b | onDeact b | own1 b | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
		{"reload ok", []Facet{facet("a", lifecycleOpt{provide: &a}), facet("b", lifecycleOpt{use: &a})}, []Facet{facet("b", lifecycleOpt{use: &a})},
			"disposed", "activate a | activate b | activate b | own2 b | onDeact b | own1 b | own2 b | onDeact b | own1 b | own2 a | onDeact a | own1 a"},
	} {
		log = nil
		var outcome []string
		host, err := CreateFacetHost(t.Context(), FacetOptions{Facets: c.facets})
		if err != nil {
			outcome = append(outcome, "threw: "+formatFacetError(err))
		} else {
			if c.reload != nil {
				if err := host.Reload(t.Context(), c.reload); err != nil {
					outcome = append(outcome, "reload threw: "+formatFacetError(err))
				}
			}
			if err := host.Dispose(t.Context()); err != nil {
				outcome = append(outcome, "dispose threw: "+formatFacetError(err))
			} else {
				outcome = append(outcome, "disposed")
			}
		}
		if got := strings.Join(outcome, " | "); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
		if got := strings.Join(log, " | "); got != c.log {
			t.Errorf("%s log:\n got %s\nwant %s", c.name, got, c.log)
		}
	}
}
