package cli

import (
	"context"
	"net"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension/host/subprocess"
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// fusedStub is a Piglet Binary's fused resolver with one member, "fusedgap", served through the SDK as fusepack serves a fused factory.
type fusedStub struct{ built atomic.Int32 }

func (r *fusedStub) FusedServe(cfg subprocess.ExtConfig) (func(net.Conn) error, bool) {
	if cfg.Name != "fusedgap" {
		return nil, false
	}
	return func(conn net.Conn) error {
		defer func() { _ = conn.Close() }()
		return sdk.ServeFactory(conn, func() *sdk.Extension {
			r.built.Add(1)
			return sdk.New("fusedgap")
		})
	}, true
}

// childOver creates a child session over parent with resources and returns it with the paths of its extensions and its load errors.
func childOver(t *testing.T, parent *coding.AgentSessionServices, resources *subprocess.ChildSessionResources) (*childSession, []string, []string) {
	t.Helper()
	child, err := newChildSession(context.Background(), parent, subprocess.ChildSessionRequest{InMemory: true, Resources: resources})
	if err != nil {
		t.Fatalf("newChildSession: %v", err)
	}
	c := child.(*childSession)
	t.Cleanup(func() { c.Dispose(); c.services.Close() })
	return c, c.session.ExtensionRunner().ExtensionPaths(), c.Info().ExtensionErrors
}

func fusedTestParent(t *testing.T) *coding.AgentSessionServices {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("PIG_HOME", dir+"/home")
	t.Setenv("PIG_CODING_AGENT_DIR", dir+"/agent")
	parent, err := coding.CreateAgentSessionServices(coding.CreateAgentSessionServicesOptions{CWD: dir, AgentDir: dir + "/agent"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(parent.Close)
	return parent
}

// A child session loads a member fused into the Piglet Binary when its resources name it by `builtin:<member>` in AdditionalExtensionPaths, as Pi's
// `-e builtin:<name>` names a built-in extension (package-manager.ts:1067, resource-loader.ts:758-775). It gets none without the path, so a
// member that creates children is not loaded again in each of them, and any other `builtin:` name is the unknown built-in error it is without fusion.
func TestChildSessionLoadsAFusedMemberNamedByABuiltinPath(t *testing.T) {
	parent := fusedTestParent(t)
	stub := &fusedStub{}
	subprocess.SetFusedResolver(stub)
	t.Cleanup(func() { subprocess.SetFusedResolver(nil) })
	member := "builtin:fusedgap"

	t.Run("without the path the child has none", func(t *testing.T) {
		before := stub.built.Load()
		for _, resources := range []*subprocess.ChildSessionResources{nil, {}} {
			if _, paths, errs := childOver(t, parent, resources); len(paths) != 0 || len(errs) != 0 {
				t.Fatalf("child extensions %q, errors %q, want none", paths, errs)
			}
		}
		if got := stub.built.Load(); got != before {
			t.Fatalf("the member was built %d times for children that did not name it", got-before)
		}
	})

	t.Run("with the path the child loads it once", func(t *testing.T) {
		before := stub.built.Load()
		child, paths, errs := childOver(t, parent, &subprocess.ChildSessionResources{AdditionalExtensionPaths: []string{member}})
		if !slices.Equal(paths, []string{member}) || len(errs) != 0 {
			t.Fatalf("child extensions %q, errors %q, want [%s] and no errors", paths, errs, member)
		}
		if got := stub.built.Load() - before; got != 1 {
			t.Fatalf("the member was built %d times, want once", got)
		}
		// The child of that child inherits nothing: it has the member only when it names it again.
		before = stub.built.Load()
		if _, paths, _ := childOver(t, child.services, nil); len(paths) != 0 || stub.built.Load() != before {
			t.Fatalf("the child's child has %q (built %d more times), want none", paths, stub.built.Load()-before)
		}
		if _, paths, _ := childOver(t, child.services, &subprocess.ChildSessionResources{AdditionalExtensionPaths: []string{member}}); !slices.Equal(paths, []string{member}) {
			t.Fatalf("the child's child that names the member has %q", paths)
		}
	})

	// The name filter is Pi's extensionsOverride on the extension's path, which a fused member shows as `builtin:<name>`: a name that is part of
	// that path keeps it, any other drops it, and noExtensions keeps what AdditionalExtensionPaths names.
	t.Run("the name filter reads the builtin path", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			resources subprocess.ChildSessionResources
			want      []string
		}{
			{"named", subprocess.ChildSessionResources{ExtensionNames: &[]string{"fusedgap"}}, []string{member}},
			{"named without case", subprocess.ChildSessionResources{ExtensionNames: &[]string{"FusedGap"}}, []string{member}},
			{"other name", subprocess.ChildSessionResources{ExtensionNames: &[]string{"other"}}, nil},
			{"no names", subprocess.ChildSessionResources{ExtensionNames: &[]string{}}, nil},
			{"noExtensions", subprocess.ChildSessionResources{NoExtensions: true}, []string{member}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				resources := tc.resources
				resources.AdditionalExtensionPaths = []string{member}
				if _, paths, _ := childOver(t, parent, &resources); !slices.Equal(paths, tc.want) {
					t.Fatalf("child extensions %q, want %q", paths, tc.want)
				}
			})
		}
	})

	t.Run("another builtin name is unknown", func(t *testing.T) {
		_, paths, errs := childOver(t, parent, &subprocess.ChildSessionResources{AdditionalExtensionPaths: []string{"builtin:nope"}})
		if len(paths) != 0 || len(errs) != 1 || !strings.HasSuffix(errs[0], "Unknown built-in extension: builtin:nope") {
			t.Fatalf("child extensions %q, errors %q, want the unknown built-in error", paths, errs)
		}
	})
}

// A Binary that fuses nothing, stock PiG included, has no member behind `builtin:<name>`: the path is the unknown built-in error, as it is in Pi.
func TestChildSessionRejectsABuiltinPathWithoutFusedMembers(t *testing.T) {
	parent := fusedTestParent(t)
	subprocess.SetFusedResolver(nil)
	_, paths, errs := childOver(t, parent, &subprocess.ChildSessionResources{AdditionalExtensionPaths: []string{"builtin:fusedgap"}})
	if len(paths) != 0 || len(errs) != 1 || !strings.HasSuffix(errs[0], "Unknown built-in extension: builtin:fusedgap") {
		t.Fatalf("child extensions %q, errors %q, want the unknown built-in error", paths, errs)
	}
}
