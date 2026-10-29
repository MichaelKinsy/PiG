package link_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/next/internal/nexttest"
	"github.com/MichaelKinsy/PiG/chord/next/link"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
)

// Ports packages/chord/test/next/link/part.test.ts: the limits per part (in UTF-8 bytes), compression at the origin,
// opening a part where it is owned, and the asynchronous inflate with its limit, its corrupt outcome and its stop
// when the link closes.

var none = link.Limits{Part: math.Inf(1), Decoded: math.Inf(1)}

// jsonOf is a JSON text of length characters that deflates well.
func jsonOf(length int) string {
	text, _ := json.Marshal(strings.Repeat("x", length-2))
	return string(text)
}

func counts(meter *nexttest.Meter, name string) (float64, bool) {
	value, ok := meter.Counts()[name]
	return value, ok
}

func TestLimitsPerPart(t *testing.T) {
	t.Run("default to none on trusted links and to 16 MiB on the wire, 64 MiB decoded on untrusted ones", func(t *testing.T) {
		if got := link.LinkLimits(true, link.PartLimits{}); got != none {
			t.Fatalf("trusted = %+v", got)
		}
		const mib = 1024 * 1024
		if got := link.LinkLimits(false, link.PartLimits{}); got != (link.Limits{Part: 16 * mib, Decoded: 64 * mib}) {
			t.Fatalf("untrusted = %+v", got)
		}
		if got := link.LinkLimits(false, link.PartLimits{Part: new(float64(10))}); got != (link.Limits{Part: 10, Decoded: 64 * mib}) {
			t.Fatalf("part 10 = %+v", got)
		}
		if got := link.LinkLimits(true, link.PartLimits{Decoded: new(float64(5))}); got != (link.Limits{Part: math.Inf(1), Decoded: 5}) {
			t.Fatalf("decoded 5 = %+v", got)
		}
	})

	t.Run("check a part's size on the wire with one comparison, nothing parsed", func(t *testing.T) {
		if err := link.Oversized(link.Text("[1,2]"), 5); err != nil {
			t.Fatalf("within: %v", err)
		}
		err := link.Oversized(link.Text("[1,2,3]"), 5)
		if err == nil {
			t.Fatal("no error")
		}
		nexttest.RequireJSONEqual(t, err.ToJSON(), object("code", "too_large", "reason", "part", "message", "a part of 7 bytes is over the wire limit of 5 bytes"))
		if over := link.Oversized(link.Bytes(make([]byte, 6)), 5); over == nil || over.Code != data.CodeTooLarge {
			t.Fatalf("bytes: %v", over)
		}
		if over := link.Oversized(link.NewDeflatedPart(make([]byte, 5)), 5); over != nil {
			t.Fatalf("deflated within: %v", over)
		}
	})

	t.Run("count text in UTF-8 bytes, not UTF-16 units (a text link bounds what a binary link does)", func(t *testing.T) {
		text, _ := json.Marshal(strings.Repeat("界", 6000))
		if len(text) != 18_002 {
			t.Fatalf("bytes = %d", len(text))
		}
		if err := link.Oversized(link.Text(text), 18_002); err != nil {
			t.Fatalf("at the limit: %v", err)
		}
		over := link.Oversized(link.Text(text), 18_001)
		if over == nil || over.Message != "a part of 18002 bytes is over the wire limit of 18001 bytes" {
			t.Fatalf("over = %v", over)
		}
		// Two-byte characters and surrogate pairs (four bytes for two units).
		if err := link.Oversized(link.Text(`"é😀"`), 8); err != nil {
			t.Fatalf("é😀 within: %v", err)
		}
		if over := link.Oversized(link.Text(`"é😀"`), 7); over == nil || over.Code != data.CodeTooLarge {
			t.Fatalf("é😀 over: %v", over)
		}
	})
}

func TestCompressionAtTheOrigin(t *testing.T) {
	t.Run("deflates a JSON part above 16 KiB when the host compresses, and counts it", func(t *testing.T) {
		meter := nexttest.Metered(t)
		small := jsonOf(link.CompressAbove / 3)
		if part := link.OriginPart(small, nextnode.NodeCompression, meter.Context); part != link.Text(small) {
			t.Fatalf("small = %T", part)
		}
		large := jsonOf(link.CompressAbove + 1)
		part, ok := link.OriginPart(large, nextnode.NodeCompression, meter.Context).(*link.DeflatedPart)
		if !ok {
			t.Fatal("large is not deflated")
		}
		if len(part.Bytes) >= 200 {
			t.Fatalf("deflated bytes = %d", len(part.Bytes))
		}
		if got, _ := counts(meter, "chord.link.deflates"); got != 1 {
			t.Fatalf("deflates = %v", got)
		}
	})

	t.Run("measures the threshold in UTF-8 bytes: a text of few units but many bytes is compressed", func(t *testing.T) {
		meter := nexttest.Metered(t)
		text, _ := json.Marshal(strings.Repeat("界", 6000))
		if len([]rune(string(text))) >= link.CompressAbove {
			t.Fatal("not few units")
		}
		if _, ok := link.OriginPart(string(text), nextnode.NodeCompression, meter.Context).(*link.DeflatedPart); !ok {
			t.Fatal("not compressed")
		}
		// Within the threshold in bytes: its bytes, encoded once (the codec copies them).
		within := jsonOf(link.CompressAbove)
		got, ok := link.OriginPart(within, nextnode.NodeCompression, meter.Context).(link.Bytes)
		if !ok || !bytes.Equal(got, []byte(within)) {
			t.Fatalf("within = %T", got)
		}
	})

	t.Run("sends a part as it is without a compressor, and its bytes when deflating does not make it smaller", func(t *testing.T) {
		meter := nexttest.Metered(t)
		large := jsonOf(link.CompressAbove * 2)
		if part := link.OriginPart(large, nil, meter.Context); part != link.Text(large) {
			t.Fatalf("without a compressor = %T", part)
		}
		got, ok := link.OriginPart(large, useless{}, meter.Context).(link.Bytes)
		if !ok || !bytes.Equal(got, []byte(large)) {
			t.Fatalf("useless = %T", got)
		}
	})
}

// useless is a Compression whose deflate does not make anything smaller.
type useless struct{}

func (useless) Deflate(input []byte) []byte { return input }

func (useless) Inflate(input []byte, _ float64) link.Inflated {
	return link.Inflated{Kind: nextnode.InflatedBytes, Bytes: input}
}

func deflated(t testing.TB, input []byte) *link.DeflatedPart {
	t.Helper()
	return link.NewDeflatedPart(nextnode.NodeCompression.Deflate(input))
}

// openedText is the text of a part opened.
func openedText(t testing.TB, part link.Part) string {
	t.Helper()
	switch typed := part.(type) {
	case link.Text:
		return string(typed)
	case link.Bytes:
		return string(typed)
	}
	t.Fatalf("part %T is not opened", part)
	return ""
}

func TestOpeningAPart(t *testing.T) {
	t.Run("returns a plain part within its limits; past either it throws too_large/part", func(t *testing.T) {
		meter := nexttest.Metered(t)
		opened, err := link.OpenPart(link.Text("[1]"), none, nil, meter.Context)
		requireNoError(t, err)
		if opened != link.Text("[1]") {
			t.Fatalf("opened = %v", opened)
		}
		_, err = link.OpenPart(link.Text("[1,2]"), link.Limits{Part: 4, Decoded: 100}, nil, meter.Context)
		requireErrorContains(t, err, "a part of 5 bytes is over the wire limit of 4 bytes")
		// Not deflated: its decoded size is its size.
		_, err = link.OpenPart(link.Bytes("[1,2]"), link.Limits{Part: 100, Decoded: 4}, nil, meter.Context)
		requireErrorContains(t, err, "a part of 5 bytes is over the decoded limit of 4 bytes")
	})

	t.Run("inflates a deflated part synchronously within the decoded limit, and counts it", func(t *testing.T) {
		meter := nexttest.Metered(t)
		text := jsonOf(100_000)
		part := deflated(t, []byte(text))
		opened, err := link.OpenPart(part, link.Limits{Part: 1000, Decoded: 100_000}, nextnode.NodeCompression, meter.Context)
		requireNoError(t, err)
		if openedText(t, opened) != text {
			t.Fatal("inflated text differs")
		}
		if got, _ := counts(meter, "chord.link.inflates"); got != 1 {
			t.Fatalf("inflates = %v", got)
		}
	})

	t.Run("refuses a compression bomb: a small part inflating past the decoded limit", func(t *testing.T) {
		meter := nexttest.Metered(t)
		bomb := deflated(t, make([]byte, 10*1024*1024))
		if len(bomb.Bytes) >= 50_000 {
			t.Fatalf("bomb bytes = %d", len(bomb.Bytes))
		}
		_, err := link.OpenPart(bomb, link.Limits{Part: 1_000_000, Decoded: 1024 * 1024}, nextnode.NodeCompression, meter.Context)
		chordErr, ok := data.IsChordErrorCode(err, data.CodeTooLarge)
		if !ok || chordErr.Reason != data.ReasonPart {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("checks the wire size before inflating anything", func(t *testing.T) {
		meter := nexttest.Metered(t)
		part := deflated(t, []byte(jsonOf(100_000)))
		_, err := link.OpenPart(part, link.Limits{Part: 10, Decoded: 1_000_000}, nextnode.NodeCompression, meter.Context)
		if _, ok := data.IsChordErrorCode(err, data.CodeTooLarge); !ok {
			t.Fatalf("error = %v", err)
		}
		requireErrorContains(t, err, "wire limit")
		if _, ok := counts(meter, "chord.link.inflates"); ok {
			t.Fatal("inflated before checking the wire size")
		}
	})

	t.Run("opens what an asynchronous inflate left on the part", func(t *testing.T) {
		meter := nexttest.Metered(t)
		part := deflated(t, []byte(jsonOf(1000)))
		link.InflateAsync(part, 2000, context.Background(), meter.Context)
		opened, err := link.OpenPart(part, link.Limits{Part: 1000, Decoded: 2000}, nil, meter.Context)
		requireNoError(t, err)
		if len(openedText(t, opened)) != 1000 {
			t.Fatalf("length = %d", len(openedText(t, opened)))
		}
		bomb := deflated(t, []byte(jsonOf(1000)))
		link.InflateAsync(bomb, 999, context.Background(), meter.Context)
		_, err = link.OpenPart(bomb, link.Limits{Part: 1000, Decoded: 999}, nil, meter.Context)
		requireErrorContains(t, err, "decoded limit of 999")
	})

	t.Run("throws a plain error for a deflated part nothing inflated, or no raw deflate stream (a protocol error)", func(t *testing.T) {
		meter := nexttest.Metered(t)
		part := link.NewDeflatedPart([]byte{255, 255, 255})
		_, err := link.OpenPart(part, none, nil, meter.Context)
		requireErrorContains(t, err, "a deflated part this host did not inflate")
		_, err = link.OpenPart(part, none, nextnode.NodeCompression, meter.Context)
		requireErrorContains(t, err, "no raw deflate stream")
		if _, ok := data.IsChordError(err); ok {
			t.Fatalf("a corrupt part is a plain error, got %v", err)
		}
	})
}

func TestTheAsynchronousInflate(t *testing.T) {
	never := context.Background()

	t.Run("inflates with a stream, counting output bytes", func(t *testing.T) {
		meter := nexttest.Metered(t)
		text := jsonOf(200_000)
		part := deflated(t, []byte(text))
		link.InflateAsync(part, math.Inf(1), never, meter.Context)
		if part.Inflated == nil || part.Inflated.Kind != nextnode.InflatedBytes {
			t.Fatalf("inflated = %+v", part.Inflated)
		}
		opened, err := link.OpenPart(part, none, nil, meter.Context)
		requireNoError(t, err)
		if openedText(t, opened) != text {
			t.Fatal("inflated text differs")
		}
		if got, _ := counts(meter, "chord.link.inflates"); got != 1 {
			t.Fatalf("inflates = %v", got)
		}
	})

	t.Run("stops past the limit: a bomb leaves too_large, never the whole output; a limit of 0 too", func(t *testing.T) {
		meter := nexttest.Metered(t)
		bomb := deflated(t, make([]byte, 64*1024*1024))
		link.InflateAsync(bomb, 1024*1024, never, meter.Context)
		if bomb.Inflated == nil || bomb.Inflated.Kind != nextnode.InflatedTooLarge || bomb.Inflated.Bytes != nil {
			t.Fatalf("bomb = %+v", bomb.Inflated)
		}
		one := deflated(t, make([]byte, 1))
		link.InflateAsync(one, 0, never, meter.Context)
		if one.Inflated == nil || one.Inflated.Kind != nextnode.InflatedTooLarge {
			t.Fatalf("one = %+v", one.Inflated)
		}
	})

	t.Run("leaves corrupt for data that is no raw deflate stream", func(t *testing.T) {
		meter := nexttest.Metered(t)
		part := link.NewDeflatedPart([]byte{255, 255, 255})
		link.InflateAsync(part, 100, never, meter.Context)
		if part.Inflated == nil || part.Inflated.Kind != nextnode.InflatedCorrupt {
			t.Fatalf("inflated = %+v", part.Inflated)
		}
	})

	t.Run("stops at once when its link closes (the signal), leaving no outcome", func(t *testing.T) {
		meter := nexttest.Metered(t)
		closing, closeLink := context.WithCancel(context.Background())
		part := deflated(t, make([]byte, 64*1024*1024))
		done := make(chan struct{})
		go func() {
			link.InflateAsync(part, math.Inf(1), closing, meter.Context)
			close(done)
		}()
		closeLink()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("the inflate did not stop")
		}
		if part.Inflated != nil {
			t.Fatalf("inflated = %+v", part.Inflated)
		}
	})
}
