// Package chordview shows the client surfaces of a chord app to the clients of a plugin UI registry: one panel per
// surface, mounted under the plugin name "chord". pig additive (D614).
//
// It is the host's side of the pairing: it imports chord and coding/uiview, and chord imports neither (the import
// guard checks that). A panel says which surface it is and the hash of the surface's base bundle; it carries no intent.
package chordview

import (
	"encoding/json"
	"sync"

	"github.com/MichaelKinsy/PiG/chord/nextnode/app"
	"github.com/MichaelKinsy/PiG/coding/uiview"
)

// Plugin is the plugin name the surfaces are mounted under. pig additive (D614): the name is the host's; an extension of
// that name mounts nothing (coding/extension/host/subprocess).
const Plugin = "chord"

// Surface is one client surface of a chord app: its name and the hash of its base bundle, "" until it is built.
type Surface struct {
	Name string
	Base func() string
}

// Surfaces returns the client surfaces a has: the web's and the terminal's.
func Surfaces(a *app.App) []Surface {
	var out []Surface
	if a.Web != nil {
		out = append(out, Surface{Name: "web", Base: a.Web.Base})
	}
	if a.Tui != nil {
		out = append(out, Surface{Name: "tui", Base: a.Tui.Base})
	}
	return out
}

// Views are the panels of one app's surfaces in one registry.
type Views struct {
	r        *uiview.Registry
	surfaces []Surface
	done     chan struct{}

	mu     sync.Mutex
	refs   map[string]uiview.Ref
	shown  map[string]string
	closed bool
	// unfollow ends Follow.
	unfollow func()
}

// Show returns the panels of surfaces in r. Nothing is mounted until Refresh.
func Show(r *uiview.Registry, surfaces []Surface) *Views {
	return &Views{r: r, surfaces: surfaces, done: make(chan struct{}), refs: map[string]uiview.Ref{}, shown: map[string]string{}}
}

// Follow refreshes the panels whenever the abilities of the registry's clients change, until Close: a client that
// attaches after the surfaces were shown is sent their panels without anyone calling Refresh. The plugin name chord has
// no extension process, so this is the host's form of the SDKs' abilities event.
func (v *Views) Follow() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed || v.unfollow != nil {
		return
	}
	v.unfollow = v.r.OnAbilities(func() { _ = v.Refresh() })
}

// ShowApp mounts the panels of a's surfaces in r, updates them once a's bases were built, and follows the registry's
// clients. While no client draws panels the mounts fail unsupported, as every mount does; they are made when a client
// that draws panels attaches.
func ShowApp(r *uiview.Registry, a *app.App) *Views {
	v := Show(r, Surfaces(a))
	v.Follow()
	_ = v.Refresh()
	go func() {
		select {
		case <-a.BasesBuilt():
			_ = v.Refresh()
		case <-v.done:
		}
	}()
	return v
}

type textNode struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type view struct {
	Root textNode `json:"root"`
}

// textView is the view of a panel: one text node.
func textView(text string) json.RawMessage {
	raw, _ := json.Marshal(view{Root: textNode{Kind: "text", Text: text}})
	return raw
}

func text(s Surface) string {
	base := s.Base()
	if base == "" {
		return "chord " + s.Name + ": base not built"
	}
	return "chord " + s.Name + ": base " + base
}

// Refresh mounts the surfaces that have no panel and replaces the panel of each surface whose base changed. A panel the
// host ended (a replaced session, a shutdown) is mounted again. It returns the first error; the other surfaces are
// still tried.
func (v *Views) Refresh() *uiview.Error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	var first *uiview.Error
	for _, s := range v.surfaces {
		want := text(s)
		ref, mounted := v.refs[s.Name]
		if mounted {
			// A panel the host ended is mounted again.
			if st, err := v.r.State(Plugin, uiview.StateArgs{Key: s.Name}); err != nil || st.Mount.Epoch != ref.Epoch || (st.State != uiview.StateLive && st.State != uiview.StateHidden) {
				mounted = false
				delete(v.refs, s.Name)
			}
		}
		if mounted && v.shown[s.Name] == want {
			continue
		}
		content := uiview.Content{View: textView(want)}
		var st uiview.Status
		var err *uiview.Error
		if mounted {
			st, err = v.update(ref, content)
			if err != nil && (err.Code == uiview.CodeTerminal || err.Code == uiview.CodeNotFound || err.Code == uiview.CodeStale) {
				mounted = false
				delete(v.refs, s.Name)
			}
		}
		if !mounted {
			st, err = v.mount(s.Name, content)
		}
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		v.refs[s.Name], v.shown[s.Name] = st.Mount, want
	}
	return first
}

// mount mounts a panel under key. The record is built here, not decoded, so it is checked as the decoder checks one.
func (v *Views) mount(key string, content uiview.Content) (uiview.Status, *uiview.Error) {
	args := uiview.MountArgs{Key: key, Slot: uiview.SlotPanel, Content: content}
	if err := args.Check(); err != nil {
		return uiview.Status{}, err
	}
	return v.r.Mount(Plugin, args)
}

// update replaces the content of the panel ref names, checked as mount checks it.
func (v *Views) update(ref uiview.Ref, content uiview.Content) (uiview.Status, *uiview.Error) {
	args := uiview.UpdateArgs{Mount: ref, Content: content}
	if err := args.Check(); err != nil {
		return uiview.Status{}, err
	}
	return v.r.Update(Plugin, args)
}

// Close finishes the panels: the app closed.
func (v *Views) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return
	}
	v.closed = true
	close(v.done)
	if v.unfollow != nil {
		v.unfollow()
	}
	for _, ref := range v.refs {
		_, _ = v.r.Finish(Plugin, uiview.FinishArgs{Mount: ref})
	}
}
