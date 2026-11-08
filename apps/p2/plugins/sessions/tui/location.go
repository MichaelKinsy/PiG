package tui

// Where a session runs, in the TUI: `[env:]path` as the footer writes it (`gpu:/data`; no prefix is this device), the environments to choose from (`Envs`, owner)
// and directory completion from `Envs.listDirectory`, as the web's DirectoryPicker lists folders. Used by `/cd` (relocate), `/new` and the "+ New session"
// prompt.
//
// Ports apps/p2/plugins/sessions/tui-location.ts.

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/envs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	pitui "github.com/MichaelKinsy/PiG/tui"
	"github.com/MichaelKinsy/PiG/tui/widthx"
)

// LocationLabel is the footer's form of a location.
func LocationLabel(location p2runtime.LocationInput) string {
	if location.Env != nil && *location.Env != "" {
		return *location.Env + ":" + location.Path
	}
	return location.Path
}

// holding is one held `Envs` per host while it is in use, released after 30 s without a request. Its environment list is subscribed while held (a state's value
// arrives only with a subscription).
type holding struct {
	ref   *host.Reference
	timer *time.Timer
	done  chan struct{}
	once  sync.Once
	names []string
	stop  func()
}

var (
	heldMu sync.Mutex
	held   = map[host.Host]*holding{}
)

func hold(ctx runtime.TuiContext) *holding {
	heldMu.Lock()
	defer heldMu.Unlock()
	h := ctx.Host()
	entry := held[h]
	if entry == nil || p2runtime.StatusOf(entry.ref) == host.RefStateEnded {
		ref, err := h.Hold(context.Background(), contract.Envs, delta.JsonObjectOf(), host.GetOptions{})
		created := &holding{done: make(chan struct{}), stop: func() {}}
		if err != nil {
			created.once.Do(func() { close(created.done) })
		} else {
			created.ref = ref
			go func() {
				resolve := func(names []string) {
					created.once.Do(func() { created.names = names; close(created.done) })
				}
				// Nothing to choose while the list cannot be read (not the owner, a failure): this device only.
				timer := time.AfterFunc(5*time.Second, func() { resolve(nil) })
				defer timer.Stop()
				if err := p2runtime.WhenLive(context.Background(), ref); err != nil {
					resolve(nil)
					return
				}
				if variant, _, _ := ref.Variant(); p2runtime.StatusOf(ref) != host.RefStateLive || variant != "owner" {
					resolve(nil)
					return
				}
				// The environments' connections start now, as the web's picker does: the first listing need not wait.
				go func() { _, _ = ref.Call("warm", delta.JsonObjectOf(), context.Background()) }()
				created.stop = p2tui.FollowMember(ref.Member("envs"), func(value delta.JsonValue) {
					if value == nil {
						return
					}
					var names []string
					for _, each := range p2tui.JList(value) {
						names = append(names, p2tui.JStr(p2tui.JField(each, "name")))
					}
					resolve(names)
				})
			}()
		}
		entry = created
		held[h] = created
	}
	if entry.timer != nil {
		entry.timer.Stop()
	}
	current := entry
	current.timer = time.AfterFunc(30*time.Second, func() {
		heldMu.Lock()
		if held[h] == current {
			delete(held, h)
		}
		heldMu.Unlock()
		current.stop()
		if current.ref != nil {
			current.ref.Release()
		}
	})
	return current
}

// OwnerEnvs is the owner's `Envs` (the first use starts the environments' SSH connections, as the web's picker does); it blocks until it is live.
func OwnerEnvs(ctx runtime.TuiContext) (*host.Reference, error) {
	entry := hold(ctx)
	if entry.ref == nil {
		return nil, errors.New("Environments are owner-only")
	}
	if err := p2runtime.WhenLive(context.Background(), entry.ref); err != nil {
		return nil, err
	}
	if variant, _, _ := entry.ref.Variant(); p2runtime.StatusOf(entry.ref) != host.RefStateLive || variant != "owner" {
		return nil, errors.New("Environments are owner-only")
	}
	return entry.ref, nil
}

// EnvNames are the configured environment names (none while they cannot be read); it blocks.
func EnvNames(ctx runtime.TuiContext) []string {
	entry := hold(ctx)
	select {
	case <-entry.done:
		return entry.names
	case <-time.After(6 * time.Second):
		return nil
	}
}

var separators = regexp.MustCompile(`[\\/~]`)

// CompleteLocation completes a `[env:]path` argument: environment names while no path is typed, then the subdirectories of the typed directory whose names start
// with the rest (case-insensitive), as `Envs.listDirectory` lists them. It blocks.
func CompleteLocation(ctx runtime.TuiContext, prefix string, fallback p2runtime.LocationFallback) []pitui.AutocompleteItem {
	names := EnvNames(ctx)
	matchEnv, _, hasMatch := p2runtime.EnvPrefix(prefix)
	var envItems []pitui.AutocompleteItem
	if !hasMatch && !separators.MatchString(prefix) {
		for _, name := range names {
			if strings.HasPrefix(name, prefix) {
				envItems = append(envItems, pitui.AutocompleteItem{Value: name + ":", Label: name + ":", Description: "environment"})
			}
		}
	}
	head := ""
	if hasMatch && (slices.Contains(names, matchEnv) || matchEnv == p2runtime.LocalEnv) {
		head = matchEnv + ":"
	}
	rest := prefix
	if head != "" {
		rest = prefix[len(head):]
	}
	cut := max(strings.LastIndex(rest, "/"), strings.LastIndex(rest, `\`))
	dir := ""
	if cut >= 0 {
		dir = rest[:cut+1]
	}
	name := strings.ToLower(rest[cut+1:])
	location, err := p2runtime.ParseLocation(head+dir, names, fallback)
	if err != nil {
		return envItems
	}
	var directories []string
	if envs, err := OwnerEnvs(ctx); err == nil {
		pairs := []any{"path", location.Path}
		if location.Env != nil && *location.Env != "" {
			pairs = append(pairs, "env", *location.Env)
		}
		if listing, err := envs.Call("listDirectory", delta.JsonObjectOf(pairs...), context.Background()); err == nil {
			for _, each := range p2tui.JList(p2tui.JField(listing, "directories")) {
				directories = append(directories, p2tui.JStr(each))
			}
		}
	}
	items := envItems
	count := 0
	for _, path := range directories {
		parts := strings.FieldsFunc(path, func(r rune) bool { return r == '/' || r == '\\' })
		entry := path
		if len(parts) > 0 {
			entry = parts[len(parts)-1]
		}
		if strings.HasPrefix(strings.ToLower(entry), name) && (strings.HasPrefix(name, ".") || !strings.HasPrefix(entry, ".")) {
			if count == 30 {
				break
			}
			count++
			items = append(items, pitui.AutocompleteItem{Value: head + dir + entry + "/", Label: entry + "/"})
		}
	}
	return items
}

// FillInput replaces an input's text with the cursor at its end (pi-tui's `setValue` keeps the cursor where it was).
func FillInput(input *pitui.TextInput, text string) {
	input.SetValue("")
	for _, char := range text {
		input.HandleInput(string(char))
	}
}

func whereText(env *string) string {
	if env != nil && *env != "" {
		return "on " + *env + " (remote)"
	}
	return "on this device"
}

// LocationInputOptions are the options of NewLocationInput.
type LocationInputOptions struct {
	Title string
	// Action is the Enter hint ("creates", "moves").
	Action string
	Env    *string
	Path   string
	// Moving: another environment shows the web's note on what moving means.
	Moving bool
	// MovingFrom is the session's current environment, when the prompt starts elsewhere; HasMovingFrom says it is set (default: Env).
	MovingFrom    *string
	HasMovingFrom bool
	// Error is shown under the input from the start (e.g. why the path is the home directory).
	Error    string
	OnSubmit func(location p2runtime.LocationInput)
	OnCancel func()
}

// LocationInput is a working-directory prompt with the environment chosen by Tab: this device, then each configured environment (the web DirectoryPicker's
// environment select; switching starts at that environment's home, `~`). Opening it starts the environments' connections (`Envs.warm`). Enter submits `[env:]path` as
// `ParseLocation` reads it.
type LocationInput struct {
	Input  *pitui.TextInput
	ctx    runtime.TuiContext
	opts   LocationInputOptions
	origin p2runtime.LocationFallback
	from   *string
	names  []string
	// Env is the environment chosen, nil for this device.
	Env *string
	// Error is what the last Enter refused, "" for none.
	Error string
}

func sameEnv(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// NewLocationInput is the prompt; it reads the environment names in the background and redraws.
func NewLocationInput(ctx runtime.TuiContext, options LocationInputOptions) *LocationInput {
	l := &LocationInput{Input: pitui.NewInput(pitui.InputOptions{}), ctx: ctx, opts: options, Env: options.Env, Error: options.Error}
	l.origin = p2runtime.LocationFallback{Env: options.Env, Cwd: options.Path}
	l.from = options.Env
	if options.HasMovingFrom {
		l.from = options.MovingFrom
	}
	FillInput(l.Input, options.Path)
	l.Input.OnSubmit = func(string) {
		fallback := p2runtime.LocationFallback{Env: l.Env}
		if sameEnv(l.Env, l.origin.Env) {
			fallback.Cwd = l.origin.Cwd
		}
		parsed, err := p2runtime.ParseLocation(l.Input.GetValue(), l.names, fallback)
		if err != nil {
			l.Error = err.Error()
		} else {
			options.OnSubmit(parsed)
		}
		ctx.RequestRender()
	}
	go func() {
		names := EnvNames(ctx)
		ctx.Post(func() {
			l.names = names
			ctx.RequestRender()
		})
	}()
	return l
}

// SetFocused records the focus.
func (l *LocationInput) SetFocused(focused bool) { l.Input.SetFocused(focused) }

// HandleInput routes keys.
func (l *LocationInput) HandleInput(data string) {
	keys := l.ctx.Keys()
	l.Error = ""
	switch {
	case keys.Matches(data, "tui.select.cancel"):
		l.opts.OnCancel()
	case keys.Matches(data, "tui.input.tab"):
		choices := []*string{nil}
		for i := range l.names {
			choices = append(choices, &l.names[i])
		}
		index := slices.IndexFunc(choices, func(c *string) bool { return sameEnv(c, l.Env) })
		next := choices[(index+1)%len(choices)]
		if !sameEnv(next, l.Env) {
			l.Env = next
			if sameEnv(next, l.origin.Env) && l.origin.Cwd != "" {
				FillInput(l.Input, l.origin.Cwd)
			} else {
				FillInput(l.Input, "~")
			}
		}
	default:
		l.Input.HandleInput(data)
	}
	l.ctx.RequestRender()
}

// Render draws the prompt.
func (l *LocationInput) Render(width int) []string {
	t := l.ctx.Theme().Current()
	choices := ""
	if len(l.names) > 0 {
		var lines []string
		for _, name := range l.names {
			lines = append(lines, render.LineText(name))
		}
		choices = " · " + l.ctx.KeyText("tui.input.tab") + " environment (this device, " + strings.Join(lines, ", ") + ")"
	}
	truncate := func(text string) string { return widthx.TruncateToWidth(text, width, "…", false) }
	lines := []string{truncate(t.Fg("accent", l.opts.Title+" · "+whereText(l.Env)))}
	lines = append(lines, l.Input.Render(width)...)
	if l.opts.Moving && !sameEnv(l.Env, l.from) {
		where := "on this device"
		if l.Env != nil {
			where = "on " + render.LineText(*l.Env)
		}
		lines = append(lines, truncate(t.Fg("warning", "Tools will run "+where+". The history stays; files are not copied.")))
	}
	if l.Error != "" {
		lines = append(lines, truncate(t.Fg("error", render.LineText(l.Error))))
	}
	lines = append(lines, truncate(t.Fg("dim", l.ctx.KeyText("tui.select.confirm")+" "+l.opts.Action+" · "+l.ctx.KeyText("tui.select.cancel")+" cancels"+choices)))
	return lines
}

// Invalidate invalidates the input.
func (l *LocationInput) Invalidate() { l.Input.Invalidate() }
