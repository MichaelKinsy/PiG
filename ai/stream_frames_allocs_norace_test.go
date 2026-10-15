//go:build !race && !pig_strip_mistral_conversations

package ai

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
)

// streamFrameAllocCeilings are the allocations one more delta record costs on each provider's streaming path, measured as the marginal cost between a short and a long response so the request setup cancels. They only go down: lower a ceiling with the measurement when a change makes a frame cheaper.
var streamFrameAllocCeilings = map[string]float64{
	"openai-completions-text":     49,
	"openai-completions-toolcall": 90,
	"openai-responses-text":       46,
	"openai-codex-responses-text": 58,
	"anthropic-text":              43,
	"anthropic-toolcall":          69,
	"google-text":                 85,
	"mistral-text":                58,
}

// streamFrameMallocs is the heap allocations of one whole streamed response of frames records.
func streamFrameMallocs(t *testing.T, shape streamFrameShape, frames int) uint64 {
	t.Helper()
	return streamFrameMallocsWith(t, shape, frames, StreamOptions{})
}

// streamFrameMallocsWith is streamFrameMallocs for a request with options.
func streamFrameMallocsWith(t *testing.T, shape streamFrameShape, frames int, options StreamOptions) uint64 {
	t.Helper()
	payload := shape.body(frames)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	provider := shape.provider(server.URL)
	streamFrameRun(t, provider, options) // warm the connection and any lazy tables
	best := ^uint64(0)
	for range 3 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		if events := streamFrameRun(t, provider, options); events < shape.minEvents(frames) {
			t.Fatalf("%s: %d events from %d frames", shape.name, events, frames)
		}
		runtime.ReadMemStats(&after)
		best = min(best, after.Mallocs-before.Mallocs)
	}
	return best
}

func TestStreamFrameAllocationCeilings(t *testing.T) {
	const short, long = 400, 1400
	for _, shape := range streamFrameShapes {
		ceiling, ok := streamFrameAllocCeilings[shape.name]
		if !ok {
			t.Errorf("%s has no allocation ceiling", shape.name)
			continue
		}
		perFrame := float64(streamFrameMallocs(t, shape, long)-streamFrameMallocs(t, shape, short)) / float64(long-short)
		t.Logf("%s: %.1f allocs/frame (ceiling %.1f)", shape.name, perFrame, ceiling)
		if perFrame > ceiling {
			t.Errorf("%s allocates %.1f times per frame, ceiling %.1f", shape.name, perFrame, ceiling)
		}
	}
}

// A provider stream event observer with nothing to do (a session whose extensions have no provider_stream_event handler) must not cost a parse of every wire event: a frame then costs what it costs with no observer at all. With the observer acting, the parse is back, which shows the measurement sees it.
func TestUnwantedProviderStreamEventsAreNotParsed(t *testing.T) {
	const short, long = 400, 1400
	observed := 0
	observe := func(context.Context, any, *Model) error { observed++; return nil }
	perFrame := func(shape streamFrameShape, options StreamOptions) float64 {
		return float64(streamFrameMallocsWith(t, shape, long, options)-streamFrameMallocsWith(t, shape, short, options)) / float64(long-short)
	}
	for _, shape := range streamFrameShapes {
		bare := perFrame(shape, StreamOptions{})
		observed = 0
		unwanted := perFrame(shape, StreamOptions{OnProviderStreamEvent: observe, ProviderStreamEventWanted: func() bool { return false }})
		if observed != 0 {
			t.Errorf("%s: an unwanted observer was called %d times", shape.name, observed)
		}
		wanted := perFrame(shape, StreamOptions{OnProviderStreamEvent: observe, ProviderStreamEventWanted: func() bool { return true }})
		t.Logf("%s: %.1f allocs/frame bare, %.1f unwanted, %.1f wanted", shape.name, bare, unwanted, wanted)
		if unwanted > bare+0.5 {
			t.Errorf("%s: an unwanted observer costs %.1f allocs/frame, no observer %.1f", shape.name, unwanted, bare)
		}
		if wanted < bare+3 {
			t.Errorf("%s: an acting observer costs %.1f allocs/frame, no observer %.1f; the measurement cannot see the parse", shape.name, wanted, bare)
		}
		if observed == 0 {
			t.Errorf("%s: a wanted observer was never called", shape.name)
		}
	}
}
