// Package tui is prompt templates and skills in the TUI (p2-tui-port.md 10.8): slash completion for `/name` and `/skill:name` from the viewed conversation's
// `PromptResources`, and their expansion on the session worker when the editor submits.
//
// Ports apps/p2/plugins/prompt/tui.ts.
package tui

import (
	"context"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/prompt/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Slot is the surface's slot name.
const Slot = "widget-above:prompt"

// TemplateInfo is a prompt template.
type TemplateInfo struct{ Name, Description, ArgumentHint string }

// SkillInfo is a skill.
type SkillInfo struct{ Name, Description string }

type object interface{ Value(string) any }

func text(value any) string {
	s, _ := value.(string)
	return s
}

// Surface holds the viewed conversation's `PromptResources` while this visitor may send there (no surface of its own). Its fields are the owner loop's.
type Surface struct {
	runtime   *runtime.TuiRuntime
	viewed    string
	hasViewed bool
	ref       *host.Reference
	stops     []func()
	// Templates and Skills are what the worker lists.
	Templates []TemplateInfo
	Skills    []SkillInfo
	remove    func()
}

// NewSurface is the surface of rt.
func NewSurface(rt *runtime.TuiRuntime) *Surface {
	s := &Surface{runtime: rt}
	s.remove = rt.AddListener(s.changed)
	s.changed()
	return s
}

func (s *Surface) release() {
	for _, stop := range s.stops {
		stop()
	}
	s.stops = nil
	if s.ref != nil {
		s.ref.Release()
		s.ref = nil
	}
	s.Templates, s.Skills = nil, nil
}

func (s *Surface) changed() {
	rt := s.runtime
	// Viewers cannot send, so they never hold it (the worker would refuse them).
	participant := rt.Role == runtime.RoleOwner || rt.GuestMode() == "full"
	session, conversation := "", rt.Conversation
	if participant && rt.HasSession {
		session = rt.Session
	}
	viewed := ""
	if session != "" {
		viewed = runtime.IdentityKey(session, conversation)
	}
	if viewed == s.viewed && s.hasViewed {
		return
	}
	s.release()
	s.viewed, s.hasViewed = viewed, true
	if session == "" {
		return
	}
	ref, err := rt.Host.Hold(context.Background(), contract.PromptResources, delta.JsonObjectOf("session", session, "conversation", float64(conversation)), host.GetOptions{})
	if err != nil {
		return
	}
	s.ref = ref
	follow := func(member string, apply func(list []any)) {
		source := ref.Member(member)
		if source == nil {
			return
		}
		s.stops = append(s.stops, p2tui.FollowMember(source, func(value delta.JsonValue) {
			rt.Post(func() {
				if s.ref != ref {
					return
				}
				list, _ := value.([]any)
				apply(list)
				rt.Ctx.RequestRender()
			})
		}))
	}
	follow("templates", func(list []any) {
		s.Templates = nil
		for _, each := range list {
			if o, ok := each.(object); ok {
				s.Templates = append(s.Templates, TemplateInfo{Name: text(o.Value("name")), Description: text(o.Value("description")), ArgumentHint: text(o.Value("argumentHint"))})
			}
		}
	})
	follow("skills", func(list []any) {
		s.Skills = nil
		for _, each := range list {
			if o, ok := each.(object); ok {
				s.Skills = append(s.Skills, SkillInfo{Name: text(o.Value("name")), Description: text(o.Value("description"))})
			}
		}
	})
}

// Entries are the slash entries the worker lists.
func (s *Surface) Entries() []runtime.TuiSlashEntry {
	var entries []runtime.TuiSlashEntry
	for _, template := range s.Templates {
		entries = append(entries, runtime.TuiSlashEntry{Name: template.Name, Description: render.LineText(template.Description), ArgumentHint: lineIf(template.ArgumentHint)})
	}
	for _, skill := range s.Skills {
		entries = append(entries, runtime.TuiSlashEntry{Name: "skill:" + skill.Name, Description: render.LineText(skill.Description)})
	}
	return entries
}

func lineIf(hint string) string {
	if hint == "" {
		return ""
	}
	return render.LineText(hint)
}

// Expand is the text `/name args` expands to on the session worker; ok is false without a live participant reference.
func (s *Surface) Expand(input string) (string, bool, error) {
	ref := s.ref
	if ref == nil {
		return "", false, nil
	}
	if variant, _, _ := ref.Variant(); variant != "participant" || p2runtime.StatusOf(ref) != host.RefStateLive {
		return "", false, nil
	}
	expanded, err := ref.Call("expand", delta.JsonObjectOf("text", input), context.Background())
	if err != nil {
		return "", false, err
	}
	o, _ := expanded.(object)
	return text(o.Value("text")), true, nil
}

// Render draws nothing.
func (s *Surface) Render(int) []string { return nil }

// Invalidate is a no-op.
func (s *Surface) Invalidate() {}

// Dispose releases the reference.
func (s *Surface) Dispose() {
	s.remove()
	s.release()
}

func surface(ctx runtime.TuiContext) *Surface {
	value, _ := ctx.Surface(Slot).(*Surface)
	return value
}

type source struct{}

func (source) Entries(ctx runtime.TuiContext) []runtime.TuiSlashEntry {
	if s := surface(ctx); s != nil {
		return s.Entries()
	}
	return nil
}

func (source) Expand(ctx runtime.TuiContext, input string) (string, bool, error) {
	if s := surface(ctx); s != nil {
		return s.Expand(input)
	}
	return "", false, nil
}

var _ pitui.Component = (*Surface)(nil)

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet(Slot, func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewSurface(rt) }, nil, func(facet host.FacetHost) error {
	return p2runtime.Contribute(facet, "p2.tui-prompt", func(self host.StoreSelf) error {
		p2runtime.ContributeKeyed(self, runtime.TuiSlashSources, p2runtime.Entry[runtime.TuiSlashSource]("prompt", source{}))
		return nil
	})
})
