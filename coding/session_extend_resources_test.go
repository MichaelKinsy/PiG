package coding

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/coding/extension/host/inproc"
)

// resourcesDiscoverFixture is a Session bound to a DefaultResourceLoader whose one extension returns skillDir from resources_discover (agent-session.ts extendResourcesFromExtensions).
type resourcesDiscoverFixture struct {
	session  *Session
	loader   *DefaultResourceLoader
	reasons  *[]string
	extPath  string
	skillDir string
}

func newResourcesDiscoverFixture(t *testing.T, f resourceLoaderFixture, loader func(*DefaultResourceLoader) ResourceLoader) resourcesDiscoverFixture {
	t.Helper()
	skillDir := filepath.Join(f.cwd, "extra-skills")
	writeSkill(t, skillDir, "discovered")
	extPath := filepath.Join(f.agentDir, "extensions", "discover.ts")
	var reasons []string
	ext := extension.Extension{Path: extPath}
	ext.AddEventHandler("resources_discover", 1, func(args ...any) (any, error) {
		reasons = append(reasons, args[0].(extension.ResourcesDiscoverEvent).Reason)
		return &extension.ResourcesDiscoverResult{SkillPaths: []string{filepath.Join(skillDir, "discovered")}}, nil
	})
	base := NewDefaultResourceLoader(DefaultResourceLoaderOptions{
		CWD: f.cwd, AgentDir: f.agentDir,
		LoadExtensions: func(context.Context, ExtensionLoadRequest) (LoadExtensionsResult, error) {
			return LoadExtensionsResult{Extensions: []extension.Extension{ext}}, nil
		},
	})
	if err := base.Reload(); err != nil {
		t.Fatal(err)
	}
	return resourcesDiscoverFixture{session: f.session(t, true, loader(base)), loader: base, reasons: &reasons, extPath: extPath, skillDir: skillDir}
}

// A Session builds its extension runner over resourceLoader.getExtensions() and, once session_start has been emitted, adds the resources the extensions' resources_discover handlers return to that loader and rebuilds the system prompt (agent-session.ts _buildRuntime, extendResourcesFromExtensions). The reason is "reload" for a reload and "startup" for every other session_start (agent-session.ts:3267).
func TestSessionExtendsItsLoaderFromResourcesDiscover(t *testing.T) {
	type start struct{ sessionStart, discoverReason string }
	for _, c := range []start{{"startup", "startup"}, {"reload", "reload"}, {"new", "startup"}, {"resume", "startup"}, {"fork", "startup"}} {
		t.Run(c.sessionStart, func(t *testing.T) {
			fx := newResourcesDiscoverFixture(t, newResourceLoaderFixture(t), func(l *DefaultResourceLoader) ResourceLoader { return l })
			if !fx.session.HasExtensionHandlers("resources_discover") {
				t.Fatal("the Session's runner does not hold the extensions the loader found")
			}
			if slices.Contains(skillNames(fx.loader), "discovered") || strings.Contains(fx.session.SystemPrompt(), "discovered") {
				t.Fatal("the skill is present before session_start")
			}
			fx.session.EmitSessionStart(c.sessionStart)
			if !slices.Equal(*fx.reasons, []string{c.discoverReason}) {
				t.Errorf("resources_discover reasons = %v, want [%s]", *fx.reasons, c.discoverReason)
			}
			var found *Skill
			for _, skill := range fx.loader.GetSkills().Skills {
				if skill.Name == "discovered" {
					found = skill
				}
			}
			if found == nil {
				t.Fatalf("the loader's skills = %v lack the discovered skill", skillNames(fx.loader))
			}
			if got, want := found.SourceInfo.Source, "extension:discover"; got != want {
				t.Errorf("source = %q, want %q", got, want)
			}
			if found.SourceInfo.Scope != "temporary" || found.SourceInfo.Origin != "top-level" || found.SourceInfo.BaseDir != filepath.Dir(fx.extPath) {
				t.Errorf("source info = %+v, want temporary top-level scope with the extension's directory", found.SourceInfo)
			}
			if !strings.Contains(fx.session.SystemPrompt(), "discovered") {
				t.Errorf("the system prompt was not rebuilt with the discovered skill:\n%s", fx.session.SystemPrompt())
			}
		})
	}
}

// A Session bound to NoResources has no loader to extend: its caller owns the resources and runs resources_discover itself, so the Session does not emit the event twice.
func TestSessionWithNoResourcesDoesNotEmitResourcesDiscover(t *testing.T) {
	f := newResourceLoaderFixture(t)
	var calls int
	ext := extension.Extension{Path: filepath.Join(f.agentDir, "extensions", "discover.ts")}
	ext.AddEventHandler("resources_discover", 1, func(...any) (any, error) {
		calls++
		return &extension.ResourcesDiscoverResult{}, nil
	})
	services, err := CreateAgentSessionServices(CreateAgentSessionServicesOptions{CWD: f.cwd, AgentDir: f.agentDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(services.Close)
	manager, err := NewInMemorySessionManager(f.cwd)
	if err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(services, SessionOptions{SessionManager: manager, ResourceLoader: NoResources, Runner: inproc.NewRunner([]extension.Extension{ext}, f.cwd)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if !session.HasExtensionHandlers("resources_discover") {
		t.Fatal("the supplied runner lost its handler")
	}
	session.EmitSessionStart("startup")
	if calls != 0 {
		t.Errorf("resources_discover ran %d times for a NoResources Session", calls)
	}
}

// A Session that replaces its inner session through an extension (ctx.newSession) starts the new one as a startup session_start does: the loader is extended again (agent-session-runtime.ts bindExtensions after replacement) with the "startup" reason.
func TestSessionExtendsItsLoaderAfterExtensionReplacement(t *testing.T) {
	fx := newResourcesDiscoverFixture(t, newResourceLoaderFixture(t), func(l *DefaultResourceLoader) ResourceLoader { return l })
	if _, err := fx.session.extensionNewSession(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(*fx.reasons, []string{"startup"}) || !slices.Contains(skillNames(fx.loader), "discovered") {
		t.Errorf("after replacement: reasons %v, skills %v", *fx.reasons, skillNames(fx.loader))
	}
}
