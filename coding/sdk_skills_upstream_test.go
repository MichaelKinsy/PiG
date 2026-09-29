package coding

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding/extension"
	icodingagent "github.com/MichaelKinsy/PiG/internal/codingagent"
)

// suppliedResourceLoader is the Go form of the object literal the upstream cases pass as resourceLoader.
type suppliedResourceLoader struct{ skills []*Skill }

func (l suppliedResourceLoader) GetSkills() SkillsResult {
	return SkillsResult{Skills: l.skills, Diagnostics: []extension.ResourceDiagnostic{}}
}

func (suppliedResourceLoader) GetPrompts() PromptsResult {
	return PromptsResult{Prompts: []PromptTemplate{}, Diagnostics: []extension.ResourceDiagnostic{}}
}

func (suppliedResourceLoader) GetAgentsFiles() AgentsFilesResult {
	return AgentsFilesResult{AgentsFiles: []ContextFile{}}
}

func (suppliedResourceLoader) GetSystemPrompt() (string, bool) { return "", false }

func (suppliedResourceLoader) GetAppendSystemPrompt() []string { return []string{} }

// Ports packages/coding-agent/test/sdk-skills.test.ts ("createAgentSession skills
// option"). Each case builds a Session through NewSession with cwd and agentDir
// both the temp dir, holding skills/test-skill/SKILL.md, as the upstream
// beforeEach does. The Session's own ResourceLoader is read back, as
// session.resourceLoader is upstream. The same cases run through the production
// subprocess Host in cmd/pig TestUpstreamSDKSkillsSubprocess.
func TestUpstreamSDKSkillsNativeSession(t *testing.T) {
	// upstream: packages/coding-agent/test/sdk-skills.test.ts:16
	newSession := func(t *testing.T, resourceLoader ResourceLoader) *Session {
		t.Helper()
		tempDir := t.TempDir()
		t.Setenv("PIG_HOME", filepath.Join(tempDir, "ambient"))
		skillsDir := filepath.Join(tempDir, "skills", "test-skill")
		if err := os.MkdirAll(skillsDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("---\nname: test-skill\ndescription: A test skill for SDK tests.\n---\n\n# Test Skill\n\nThis is a test skill.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		services, err := NewServices(ServicesOptions{CWD: tempDir, AgentDir: tempDir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(services.Close)
		manager, err := NewInMemorySessionManager(tempDir)
		if err != nil {
			t.Fatal(err)
		}
		session, err := NewSession(services, SessionOptions{SessionManager: manager, ResourceLoader: resourceLoader})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := session.Close(); err != nil {
				t.Error(err)
			}
		})
		return session
	}

	// upstream: packages/coding-agent/test/sdk-skills.test.ts:41
	t.Run("should discover skills by default and expose them on session.skills", func(t *testing.T) {
		session := newSession(t, nil)
		skills := session.ResourceLoader().GetSkills().Skills
		if len(skills) == 0 {
			t.Fatalf("no skills discovered: %#v", skills)
		}
		if !slices.ContainsFunc(skills, func(skill *Skill) bool { return skill.Name == "test-skill" }) {
			t.Fatalf("test-skill missing: %#v", skills)
		}
	})

	// upstream: packages/coding-agent/test/sdk-skills.test.ts:53
	t.Run("should have empty skills when resource loader returns none (--no-skills)", func(t *testing.T) {
		session := newSession(t, suppliedResourceLoader{skills: []*Skill{}})
		result := session.ResourceLoader().GetSkills()
		if !reflect.DeepEqual(result.Skills, []*Skill{}) {
			t.Fatalf("skills = %#v, want []", result.Skills)
		}
		if !reflect.DeepEqual(result.Diagnostics, []extension.ResourceDiagnostic{}) {
			t.Fatalf("diagnostics = %#v, want []", result.Diagnostics)
		}
		// The supplied loader, not discovery, decides whether a /skill: command expands.
		if got := session.expandPromptText("/skill:test-skill explain"); got != "/skill:test-skill explain" {
			t.Fatalf("a skill expanded with no skills supplied: %q", got)
		}
	})

	// upstream: packages/coding-agent/test/sdk-skills.test.ts:79
	t.Run("should use provided skills when resource loader supplies them", func(t *testing.T) {
		customSkill := &Skill{
			Name:                   "custom-skill",
			Description:            "A custom skill",
			Path:                   "/fake/path/SKILL.md",
			Dir:                    "/fake/path",
			SourceInfo:             icodingagent.PiSourceInfo{Path: "/fake/path/SKILL.md", Source: "sdk", Scope: "temporary", Origin: "top-level"},
			DisableModelInvocation: false,
		}
		session := newSession(t, suppliedResourceLoader{skills: []*Skill{customSkill}})
		result := session.ResourceLoader().GetSkills()
		if !reflect.DeepEqual(result.Skills, []*Skill{customSkill}) {
			t.Fatalf("skills = %#v, want [%#v]", result.Skills, customSkill)
		}
		if !reflect.DeepEqual(result.Diagnostics, []extension.ResourceDiagnostic{}) {
			t.Fatalf("diagnostics = %#v, want []", result.Diagnostics)
		}
	})
}

// A Session built with a supplied loader reads it on each prompt, so the loader, not the Session, owns the resources.
func TestSessionReadsSuppliedResourceLoaderLive(t *testing.T) {
	loader := &mutableSkillsLoader{}
	services := newTestServices(t)
	session, err := NewSession(services, SessionOptions{ResourceLoader: loader})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Error(err)
		}
	})
	if session.ResourceLoader() != ResourceLoader(loader) {
		t.Fatalf("ResourceLoader() = %#v, want the supplied loader", session.ResourceLoader())
	}
	if got := session.expandPromptText("/plain"); got != "/plain" {
		t.Fatalf("expanded before the loader supplied anything: %q", got)
	}
	loader.templates = []PromptTemplate{{Name: "plain", Content: "expanded"}}
	if got := session.expandPromptText("/plain"); got != "expanded" {
		t.Fatalf("expandPromptText = %q, want the template the loader now supplies", got)
	}
}

// Clone carries the loader the source Session was bound to.
func TestCloneKeepsTheBoundResourceLoader(t *testing.T) {
	h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
	h.session.SetPromptResources([]PromptTemplate{{Name: "plain", Content: "expanded"}}, nil)
	h.provider.responses = []scriptedResponse{fauxReply("ok", ai.StopReasonStop, 0)}
	if _, err := h.session.Prompt(t.Context(), "hello"); err != nil {
		t.Fatal(err)
	}
	clone, err := h.session.Clone()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := clone.Close(); err != nil {
			t.Error(err)
		}
	})
	if got := clone.expandPromptText("/plain"); got != "expanded" {
		t.Fatalf("clone expandPromptText = %q, want the source's template", got)
	}
}

type mutableSkillsLoader struct{ templates []PromptTemplate }

func (*mutableSkillsLoader) GetSkills() SkillsResult { return SkillsResult{} }
func (l *mutableSkillsLoader) GetPrompts() PromptsResult {
	return PromptsResult{Prompts: l.templates}
}
func (*mutableSkillsLoader) GetAgentsFiles() AgentsFilesResult { return AgentsFilesResult{} }
func (*mutableSkillsLoader) GetSystemPrompt() (string, bool)   { return "", false }
func (*mutableSkillsLoader) GetAppendSystemPrompt() []string   { return nil }

// Ports packages/coding-agent/test/resource-loader.test.ts:34, :831 and :855
// natively: a new DefaultResourceLoader is empty until Reload ("should
// initialize with empty results before reload"), the skills the resource owner
// supplies replace whatever discovery would find ("should apply
// skillsOverride"), and SessionOptions.SystemPrompt is the explicit system
// prompt ("should apply systemPromptOverride"). PiG's DefaultResourceLoader
// carries no extensions or themes, so the extension and theme assertions of :34
// run through the shipped Node SDK subprocess (docs/parity/resource-loader-test-review.md).
func TestUpstreamResourceLoaderNativeOverrides(t *testing.T) {
	t.Run("should initialize with empty results before reload", func(t *testing.T) {
		root := t.TempDir()
		loader := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
		// upstream: packages/coding-agent/test/resource-loader.test.ts:38-39 compare with toEqual([]).
		if skills := loader.GetSkills().Skills; !reflect.DeepEqual(skills, []*Skill{}) {
			t.Fatalf("skills = %#v, want [] before reload", skills)
		}
		if prompts := loader.GetPrompts().Prompts; !reflect.DeepEqual(prompts, []PromptTemplate{}) {
			t.Fatalf("prompts = %#v, want [] before reload", prompts)
		}
	})
	t.Run("should apply skillsOverride", func(t *testing.T) {
		injected := &Skill{Name: "injected", Description: "Injected skill", Path: "/fake/path", Dir: "/fake", SourceInfo: icodingagent.PiSourceInfo{Path: "/fake/path", Source: "custom", Scope: "temporary", Origin: "top-level"}}
		h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
		h.session.SetPromptResources(nil, []*Skill{{Name: "discovered", Description: "Discovered skill"}})
		h.session.SetPromptResources(nil, []*Skill{injected})
		skills := h.session.ResourceLoader().GetSkills().Skills
		if len(skills) != 1 || skills[0].Name != "injected" {
			t.Fatalf("skills = %#v, want only the injected skill", skills)
		}
	})
	t.Run("should apply systemPromptOverride", func(t *testing.T) {
		s, err := NewSession(newTestServices(t), SessionOptions{SystemPrompt: "Custom system prompt"})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Error(err)
			}
		})
		if got := s.SystemPrompt(); got != "Custom system prompt" {
			t.Fatalf("system prompt = %q", got)
		}
	})
}

// SetPromptResources binds a snapshot: later writes to the caller's slices do not reach the Session.
func TestSetPromptResourcesBindsASnapshot(t *testing.T) {
	h := newQueueCharacterizationHarness(t, extension.Extension{}, nil)
	templates := []PromptTemplate{{Name: "plain", Content: "expanded"}}
	skills := []*Skill{{Name: "bound", Description: "Bound skill"}}
	h.session.SetPromptResources(templates, skills)
	templates[0] = PromptTemplate{Name: "plain", Content: "rewritten"}
	skills[0] = &Skill{Name: "rewritten", Description: "Rewritten skill"}
	if got := h.session.expandPromptText("/plain"); got != "expanded" {
		t.Fatalf("expandPromptText = %q, want the template bound at SetPromptResources", got)
	}
	if got := h.session.ResourceLoader().GetSkills().Skills; len(got) != 1 || got[0].Name != "bound" {
		t.Fatalf("skills = %#v, want the skill bound at SetPromptResources", got)
	}
}

// NoResources and a new DefaultResourceLoader report empty collections, not nil, as Pi's loaders return arrays.
func TestResourceLoadersReportEmptyCollections(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PIG_HOME", filepath.Join(root, "ambient"))
	reloaded := NewDefaultResourceLoader(DefaultResourceLoaderOptions{CWD: root, AgentDir: filepath.Join(root, "agent")})
	if err := reloaded.Reload(); err != nil {
		t.Fatal(err)
	}
	for name, loader := range map[string]ResourceLoader{"NoResources": NoResources, "reloaded default": reloaded} {
		skills, prompts := loader.GetSkills(), loader.GetPrompts()
		if skills.Skills == nil || skills.Diagnostics == nil || prompts.Prompts == nil || prompts.Diagnostics == nil {
			t.Errorf("%s: skills=%#v prompts=%#v, want non-nil empty collections", name, skills, prompts)
		}
	}
}
