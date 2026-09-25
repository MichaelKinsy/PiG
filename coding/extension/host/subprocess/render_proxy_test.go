package subprocess

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding/extension"
)

func newRenderProxyRig(t *testing.T) (*renderProxyComponent, net.Conn, context.CancelFunc, *atomic.Int32) {
	t.Helper()
	hostEnd, peer := net.Pipe()
	conn := NewConn("render-proxy-test", hostEnd)
	ctx, cancel := context.WithCancel(context.Background())
	conn.Start(ctx)
	invalidated := &atomic.Int32{}
	component := newRenderProxyComponent(
		"fixture",
		"notice",
		extension.CustomMessageRef{CustomType: "notice", Content: "fallback body", Display: true},
		extension.MessageRenderOptions{},
		conn,
		time.Second,
		func() { invalidated.Add(1) },
	)
	t.Cleanup(func() {
		cancel()
		_ = peer.Close()
		_ = hostEnd.Close()
		<-conn.done
	})
	return component, peer, cancel, invalidated
}

func replyRender(t *testing.T, peer net.Conn, request Envelope, lines []string) {
	t.Helper()
	result, err := json.Marshal(RenderResult{Lines: lines})
	if err != nil {
		t.Fatal(err)
	}
	writeFramed(t, peer, Envelope{Type: MsgResponse, ID: request.ID, Response: &ResponsePayload{Result: result}})
}

func waitForRenderProxy(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("render proxy condition was not met")
}

func TestRenderProxyRenderNeverWaitsForIPC(t *testing.T) {
	component, peer, _, _ := newRenderProxyRig(t)
	start := time.Now()
	lines := component.Render(80)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Fatalf("Render blocked for %v", elapsed)
	}
	if len(lines) != 1 || lines[0] != "[notice]" {
		t.Fatalf("initial fallback = %#v", lines)
	}
	request := readFramed(t, peer)
	if request.Request == nil || request.Request.Method != "render_message" {
		t.Fatalf("request = %+v", request)
	}
}

func TestRenderProxyPublishesAsyncResultAndInvalidates(t *testing.T) {
	component, peer, _, invalidated := newRenderProxyRig(t)
	_ = component.Render(80)
	request := readFramed(t, peer)
	replyRender(t, peer, request, []string{"rendered"})

	waitForRenderProxy(t, func() bool { return invalidated.Load() > 0 })
	if got := component.Render(80); len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("cached render = %#v", got)
	}
}

func TestAC52RenderProxyRejectsStaleGeneration(t *testing.T) {
	component, peer, _, invalidated := newRenderProxyRig(t)
	_ = component.Render(80)
	first := readFramed(t, peer)
	_ = component.Render(100)
	replyRender(t, peer, first, []string{"stale-width"})
	second := readFramed(t, peer)
	var args struct {
		Width int `json:"width"`
	}
	if err := json.Unmarshal(second.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.Width != 100 {
		t.Fatalf("replacement width = %d", args.Width)
	}
	replyRender(t, peer, second, []string{"current-width"})
	waitForRenderProxy(t, func() bool { return invalidated.Load() > 0 })
	if got := component.Render(100); len(got) != 1 || got[0] != "current-width" {
		t.Fatalf("current render = %#v", got)
	}
}

func TestRenderProxyExpandedStateRefreshesOffLoop(t *testing.T) {
	component, peer, _, _ := newRenderProxyRig(t)
	_ = component.Render(80)
	first := readFramed(t, peer)
	replyRender(t, peer, first, []string{"collapsed"})
	waitForRenderProxy(t, func() bool {
		lines := component.Render(80)
		return len(lines) == 1 && lines[0] == "collapsed"
	})

	component.SetExpanded(true)
	if got := component.Render(80); len(got) != 1 || got[0] != "collapsed" {
		t.Fatalf("expanded refresh did not preserve cache: %#v", got)
	}
	second := readFramed(t, peer)
	var args struct {
		Options extension.MessageRenderOptions `json:"options"`
	}
	if err := json.Unmarshal(second.Request.Args, &args); err != nil {
		t.Fatal(err)
	}
	if !args.Options.Expanded {
		t.Fatal("expanded refresh sent collapsed options")
	}
}

func BenchmarkRenderProxyCached(b *testing.B) {
	component := newRenderProxyComponent("fixture", "notice", nil, extension.MessageRenderOptions{}, nil, time.Second, nil)
	component.width = 80
	component.lines = []string{"cached renderer line"}
	b.ReportAllocs()
	for b.Loop() {
		_ = component.Render(80)
	}
}
