// Package tui is owner diagnostics in the TUI (p2-tui-port.md P11d), after the web's logs sheet and owner notice lane: `/logs` shows a log source (server,
// terminals, dev supervisor, session workers) and follows its tail; owner notices from the server (`LogNotices`) become status lines as they arrive, and
// `/notices` lists and dismisses them. Owner only: guests never acquire these objects.
//
// Ports apps/p2/plugins/logs/tui.ts.
package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MichaelKinsy/PiG/apps/p2/plugins/logs/contract"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
	p2tui "github.com/MichaelKinsy/PiG/apps/p2/tui"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/components"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/render"
	"github.com/MichaelKinsy/PiG/apps/p2/tui/runtime"
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
	chordtui "github.com/MichaelKinsy/PiG/chord/next/tui"
	pitui "github.com/MichaelKinsy/PiG/tui"
)

// Notice is an owner notice.
type Notice struct {
	ID      int
	Time    int64
	Source  string
	Message string
}

// sourceLabel is the web's `sourceLabel`.
func sourceLabel(source string) string {
	switch {
	case strings.HasPrefix(source, "session:"):
		return "Worker " + source[8:]
	case source == "dev":
		return "Dev supervisor"
	case source == "terminals":
		return "Terminal host"
	}
	return "Server"
}

// WatchSlot is the notice watch's slot (a non-screen slot).
const WatchSlot = "service:notices"

// NoticeWatch is the owner notices as they arrive; the list for `/notices`. Its fields are the owner loop's.
type NoticeWatch struct {
	runtime    *runtime.TuiRuntime
	scope      *chordtui.TuiScope
	service    *host.Reference
	stopFollow func()
	// shown is the notices as shown (those dismissed while their calls are in flight left out), dismissing how to dismiss one.
	shown      func() []Notice
	dismissing p2tui.OptimisticAdd[int]
	received   []Notice
	seen       map[int]bool
	first      bool
	remove     func()
}

// NewNoticeWatch is the watch of rt.
func NewNoticeWatch(rt *runtime.TuiRuntime) *NoticeWatch {
	w := &NoticeWatch{runtime: rt, seen: map[int]bool{}, first: true}
	w.shown = func() []Notice { return w.received }
	w.remove = rt.AddListener(w.changed)
	w.changed()
	return w
}

func (w *NoticeWatch) changed() {
	if w.runtime.Role != runtime.RoleOwner || w.scope != nil {
		return
	}
	scope := chordtui.NewTuiScope(context.Background(), w.runtime.Host, w.runtime.Ctx.RequestRender)
	w.scope = scope
	w.shown, w.dismissing, _ = p2tui.Optimistic(scope, func() []Notice { return w.received }, func(notices []Notice, id int) []Notice {
		var kept []Notice
		for _, notice := range notices {
			if notice.ID != id {
				kept = append(kept, notice)
			}
		}
		return kept
	})
	ref, err := scope.Hold(p2runtime.LogNotices, delta.JsonObjectOf())
	if err != nil {
		return
	}
	w.service = ref
	if member := ref.Member("notices"); member != nil {
		w.stopFollow = p2tui.FollowMember(member, func(value delta.JsonValue) {
			var notices []Notice
			for _, each := range p2tui.JList(value) {
				notices = append(notices, Notice{ID: int(p2tui.JNum(p2tui.JField(each, "id"))), Time: int64(p2tui.JNum(p2tui.JField(each, "time"))), Source: p2tui.JStr(p2tui.JField(each, "source")), Message: p2tui.JStr(p2tui.JField(each, "message"))})
			}
			w.runtime.Post(func() { w.arrived(notices) })
		})
	}
}

// Notices are the notices as shown: those dismissed meanwhile left out.
func (w *NoticeWatch) Notices() []Notice { return w.shown() }

// Dismiss dismisses notice id: it leaves the list at once and comes back if the server refuses.
func (w *NoticeWatch) Dismiss(id int) {
	service, dismissing := w.service, w.dismissing
	if service == nil || dismissing == nil {
		return
	}
	if variant, _, _ := service.Variant(); variant != "owner" || p2runtime.StatusOf(service) != host.RefStateLive {
		return
	}
	go func() {
		err := p2tui.AddCall(dismissing, id, func() error {
			_, err := service.Call("dismiss", delta.JsonObjectOf("id", float64(id)), context.Background())
			return err
		})
		if err != nil {
			w.runtime.Post(func() { w.runtime.Fail(err) })
		}
	}()
}

func (w *NoticeWatch) arrived(notices []Notice) {
	w.received = notices
	var fresh []Notice
	for _, notice := range notices {
		if !w.seen[notice.ID] {
			fresh = append(fresh, notice)
		}
	}
	for _, notice := range notices {
		w.seen[notice.ID] = true
	}
	if len(fresh) == 0 {
		return
	}
	// Notices pending from before this TUI opened are summarized once; later ones show as they arrive.
	if w.first {
		plural := "s"
		if len(fresh) == 1 {
			plural = ""
		}
		w.runtime.Notify(fmt.Sprintf("%d owner notice%s · /notices", len(fresh), plural), runtime.NoticeWarning)
	} else {
		for _, notice := range fresh {
			w.runtime.Notify(sourceLabel(notice.Source)+": "+render.LineText(notice.Message), runtime.NoticeWarning)
		}
	}
	w.first = false
}

// Render draws nothing.
func (w *NoticeWatch) Render(int) []string { return nil }

// Invalidate is a no-op.
func (w *NoticeWatch) Invalidate() {}

// Dispose stops watching.
func (w *NoticeWatch) Dispose() {
	w.remove()
	if w.stopFollow != nil {
		w.stopFollow()
	}
	if w.scope != nil {
		w.scope.Dispose()
	}
}

func watchOf(ctx runtime.TuiContext) *NoticeWatch {
	watch, _ := ctx.Surface(WatchSlot).(*NoticeWatch)
	return watch
}

func clock(ms int64) string { return time.UnixMilli(ms).Format("3:04:05 PM") }

func openNotices(ctx runtime.TuiContext) error {
	watch := watchOf(ctx)
	if watch == nil {
		return nil
	}
	var handle runtime.TuiOverlayHandle
	handle = ctx.Overlay("notices", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string { return fmt.Sprintf("Owner notices (%d)", len(watch.Notices())) },
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for _, notice := range watch.Notices() {
				rows = append(rows, components.ListRow{Key: strconv.Itoa(notice.ID), Label: notice.Message, Detail: sourceLabel(notice.Source) + " " + clock(notice.Time), Description: notice.Message, Color: "warning"})
			}
			return rows
		},
		Actions: []components.ListAction{{ID: "app.session.delete", Label: "dismiss", Run: func(row *components.ListRow) {
			if row != nil {
				if id, err := strconv.Atoi(row.Key); err == nil {
					watch.Dismiss(id)
				}
			}
		}}},
		OnClose: func() { handle.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

type logLine struct {
	ID          int
	Time        int64
	Level, Text string
}

// openLogs is `/logs [source]`: a source's lines, newest at the bottom; Tab picks another source.
func openLogs(ctx runtime.TuiContext, initial string) error {
	source := strings.TrimSpace(initial)
	if source == "" {
		if session := ctx.Identity().Session; session != "" {
			source = "session:" + session
		} else {
			source = "server"
		}
	}
	var ref *host.Reference
	var lines []logLine
	var sources []string
	var stops []func()
	release := func() {
		for _, stop := range stops {
			stop()
		}
		stops = nil
		if ref != nil {
			ref.Release()
		}
	}
	acquire := func() {
		release()
		lines = nil
		current, err := ctx.Host().Hold(context.Background(), contract.Logs, delta.JsonObjectOf("source", source), host.GetOptions{})
		if err != nil {
			ctx.Fail(err)
			ref = nil
			return
		}
		ref = current
		mine := source
		stops = []func(){
			p2tui.FollowMember(current.Member("lines"), func(value delta.JsonValue) {
				ctx.Post(func() {
					if ref != current {
						return
					}
					lines = nil
					for _, each := range p2tui.JList(value) {
						lines = append(lines, logLine{ID: int(p2tui.JNum(p2tui.JField(each, "id"))), Time: int64(p2tui.JNum(p2tui.JField(each, "time"))), Level: p2tui.JStr(p2tui.JField(each, "level")), Text: p2tui.JStr(p2tui.JField(each, "text"))})
					}
					ctx.RequestRender()
				})
			}),
			p2tui.FollowMember(current.Member("sources"), func(value delta.JsonValue) {
				ctx.Post(func() {
					if ref != current {
						return
					}
					sources = nil
					add := func(id string) {
						if !slices.Contains(sources, id) {
							sources = append(sources, id)
						}
					}
					for _, id := range []string{"server", "terminals", "dev", mine} {
						add(id)
					}
					for _, each := range p2tui.JList(value) {
						add(p2tui.JStr(p2tui.JField(each, "id")))
					}
					ctx.RequestRender()
				})
			}),
		}
		go func() {
			if err := p2runtime.WhenLive(context.Background(), current); err != nil {
				ctx.Post(func() { ctx.Fail(err) })
			}
		}()
	}
	acquire()
	var overlay runtime.TuiOverlayHandle
	overlay = ctx.Overlay("logs", components.NewListOverlay(runtime.Selectors(ctx), components.ListOverlayOptions{
		Title: func() string {
			plural := "s"
			if len(lines) == 1 {
				plural = ""
			}
			return fmt.Sprintf("Logs · %s (%d line%s)", sourceLabel(source), len(lines), plural)
		},
		Rows: func() []components.ListRow {
			var rows []components.ListRow
			for _, line := range lines {
				row := components.ListRow{Key: strconv.Itoa(line.ID), Label: line.Text, Detail: line.Level + " " + clock(line.Time)}
				if line.Level == "stderr" || line.Level == "error" {
					row.Color = "error"
				}
				rows = append(rows, row)
			}
			return rows
		},
		// The newest line selected: the list follows the tail until the reader moves up.
		Follow: true,
		Height: 18,
		Actions: []components.ListAction{{ID: "tui.input.tab", Label: "next source", Run: func(*components.ListRow) {
			if len(sources) == 0 {
				return
			}
			next := sources[(slices.Index(sources, source)+1)%len(sources)]
			if next == source {
				return
			}
			source = next
			acquire()
		}}},
		OnClose: func() { release(); overlay.Close() },
	}), pitui.OverlaySpec{Width: p2tui.Percent(100), Anchor: "bottom-center"})
	return nil
}

var commands = p2runtime.Keyed[runtime.TuiCommand]{
	p2runtime.Entry("p2.logs", runtime.TuiCommand{
		Title: "Logs", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "logs", ArgumentHint: "[source]"},
		Run: func(ctx runtime.TuiContext, args string) error { return openLogs(ctx, args) },
	}),
	p2runtime.Entry("p2.notices", runtime.TuiCommand{
		Title: "Owner notices", Category: "Owner", When: "owner && !overlay", Slash: &runtime.TuiCommandSlash{Name: "notices"},
		Run: func(ctx runtime.TuiContext, _ string) error { return openNotices(ctx) },
	}),
}

// Facet is the plugin's TUI facet.
var Facet = runtime.TuiFacet(WatchSlot, func(rt *runtime.TuiRuntime) runtime.TuiSurface { return NewNoticeWatch(rt) }, commands, nil)
