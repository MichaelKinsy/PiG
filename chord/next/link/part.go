package link

import (
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"math"

	chordctx "github.com/MichaelKinsy/PiG/chord/context"
	"github.com/MichaelKinsy/PiG/chord/next/data"
	"github.com/MichaelKinsy/PiG/chord/nextnode"
	"github.com/MichaelKinsy/PiG/internal/jsstring"
	"github.com/MichaelKinsy/PiG/telemetry"
)

// Ports packages/chord/src/next/link/part.ts. Parts on links (R1, R2, Q2, §22): a value as it crosses a link (JSON
// text at its origin, its bytes once received, or deflated by its origin), the size limits per part, compression at the
// origin, and opening a part where it is owned: its size checked before anything is parsed or inflated, a deflated
// part inflated within the decoded limit.

// Part is a value crossing a link (R1): JSON text at its origin (Text), its UTF-8 bytes once a binary codec received it
// or at an origin that encoded it already (Bytes), or those bytes deflated by an origin that compresses
// (*DeflatedPart). Opaque on the way: only the end that owns it opens and parses it, while handling its message (a
// malformed part is a protocol error then), so a relay forwards it unparsed and still deflated.
type Part interface{ isPart() }

// Text is a part as JSON text.
type Text string

// Bytes is a part as UTF-8 JSON bytes (a view of the received message).
type Bytes []byte

func (Text) isPart()          {}
func (Bytes) isPart()         {}
func (*DeflatedPart) isPart() {}

// Inflated is what inflating a deflated part gave: its JSON bytes, more than the decoded limit, or data that is no
// raw deflate. It is the type of lane D1's nextnode package, which cannot import this one.
type Inflated = nextnode.Inflated

// Compression is a synchronous compressor (`createHost({ compression })`; nextnode.NodeCompression). It is the type
// of lane D1's nextnode package, which cannot import this one.
type Compression = nextnode.Compression

// DeflatedPart is a JSON part deflated (raw deflate) by its origin. A host without a synchronous compressor inflates
// it before its owner handles the message (InflateAsync) and keeps the outcome here for the owner to open.
type DeflatedPart struct {
	Bytes []byte
	// Inflated is the asynchronous inflate's outcome; nil until one ran.
	Inflated *Inflated
}

// NewDeflatedPart returns a deflated part of bytes.
func NewDeflatedPart(bytes []byte) *DeflatedPart { return &DeflatedPart{Bytes: bytes} }

// PartLimits are a link's limits per part as `accept`, `connect` and `resolve` take them, in bytes; nil is unset.
type PartLimits struct {
	// Part is a part's size on the wire.
	Part *float64
	// Decoded is a part's size after inflating it.
	Decoded *float64
}

// Limit returns a PartLimits field value.
func Limit(bytes float64) *float64 { return &bytes }

// Limits are a link's limits in force; +Inf for none.
type Limits struct{ Part, Decoded float64 }

const mib = 1024 * 1024

// untrusted are the limits of untrusted links unless set (Q2); trusted links (IPC) have none.
var untrusted = Limits{Part: 16 * mib, Decoded: 64 * mib}

var noLimits = Limits{Part: math.Inf(1), Decoded: math.Inf(1)}

// LinkLimits are the limits of a link: the ones set, the defaults of its trust for the others.
func LinkLimits(trusted bool, limits PartLimits) Limits {
	defaults := untrusted
	if trusted {
		defaults = noLimits
	}
	if limits.Part != nil {
		defaults.Part = *limits.Part
	}
	if limits.Decoded != nil {
		defaults.Decoded = *limits.Decoded
	}
	return defaults
}

// CompressAbove is the size above which JSON parts are compressed at their origin, in UTF-8 bytes (a protocol
// constant).
const CompressAbove = 16 * 1024

// OriginPart is a JSON part at its origin: deflated when the host compresses, its UTF-8 bytes are above CompressAbove
// and deflating made it smaller; otherwise the text, or its bytes once they were encoded. Counts one deflate
// (`chord.link.deflates`).
func OriginPart(json string, compression Compression, ctx context.Context) Part {
	if compression == nil {
		return Text(json)
	}
	// Upstream hands back the text itself, unencoded, when three bytes per UTF-16 unit stay within the threshold; a Go
	// string is UTF-8 already, so the bytes settle it and the units are counted only in the band the bytes leave open.
	if len(json) <= CompressAbove/3 || (len(json) <= CompressAbove && jsstring.Length(json)*3 <= CompressAbove) {
		return Text(json)
	}
	bytes := []byte(json)
	if len(bytes) <= CompressAbove {
		return Bytes(bytes)
	}
	chordctx.Telemetry(ctx).Metrics().Counter("chord.link.deflates", telemetry.InstrumentOptions{}).Add(1, nil)
	deflated := compression.Deflate(bytes)
	if len(deflated) < len(bytes) {
		return NewDeflatedPart(deflated)
	}
	return Bytes(bytes)
}

// Oversized is the `too_large/part` error of a part whose size on the wire (its UTF-8 bytes; a deflated part's
// deflated bytes) is above limit; nil within it. Nothing is parsed or inflated.
func Oversized(part Part, limit float64) *data.ChordError {
	if math.IsInf(limit, 1) {
		return nil
	}
	if size := sizeOf(part); float64(size) > limit {
		return tooLarge(size, limit, "wire")
	}
	return nil
}

// OpenPart is the JSON of a part its owner is about to parse (Text or Bytes): its size checked against limits.Part
// first, then its decoded size against limits.Decoded, a deflated part inflated within it (synchronously with
// compression, or by the asynchronous inflate that ran ahead of its owner). It returns `too_large/part` past a limit;
// a plain error for a deflated part nothing inflated or that is no raw deflate stream (a protocol error). Counts one
// synchronous inflate (`chord.link.inflates`).
func OpenPart(part Part, limits Limits, compression Compression, ctx context.Context) (Part, error) {
	if wire := Oversized(part, limits.Part); wire != nil {
		return nil, wire
	}
	deflated, isDeflated := part.(*DeflatedPart)
	if !isDeflated {
		// Not deflated: its decoded size is its size.
		if !math.IsInf(limits.Decoded, 1) {
			if size := sizeOf(part); float64(size) > limits.Decoded {
				return nil, tooLarge(size, limits.Decoded, "decoded")
			}
		}
		return part, nil
	}
	inflated := deflated.Inflated
	if inflated == nil {
		if compression == nil {
			return nil, errors.New("part: a deflated part this host did not inflate")
		}
		chordctx.Telemetry(ctx).Metrics().Counter("chord.link.inflates", telemetry.InstrumentOptions{}).Add(1, nil)
		outcome := compression.Inflate(deflated.Bytes, limits.Decoded)
		inflated = &outcome
	}
	switch inflated.Kind {
	case nextnode.InflatedBytes:
		return Bytes(inflated.Bytes), nil
	case nextnode.InflatedTooLarge:
		return nil, tooLarge(-1, limits.Decoded, "decoded")
	default:
		return nil, &causedError{message: "part: no raw deflate stream", cause: inflated.Error}
	}
}

// causedError is an error with a cause, upstream's `new Error(message, { cause })`.
type causedError struct {
	message string
	cause   error
}

func (err *causedError) Error() string { return err.message }
func (err *causedError) Unwrap() error { return err.cause }

// InflateAsync inflates part with a flate stream (a host without a synchronous compressor), counting output bytes and
// stopping past limit; the outcome is kept on the part. signal (the link's close) stops it at once, leaving no
// outcome. Counts one inflate (`chord.link.inflates`). Upstream awaits the web DecompressionStream; the Go form blocks
// until the outcome is on the part.
func InflateAsync(part *DeflatedPart, limit float64, signal context.Context, ctx context.Context) {
	chordctx.Telemetry(ctx).Metrics().Counter("chord.link.inflates", telemetry.InstrumentOptions{}).Add(1, nil)
	reader := flate.NewReader(&bytesReader{data: part.Bytes})
	defer reader.Close()
	var output []byte
	chunk := make([]byte, 32*1024)
	for {
		if signal.Err() != nil {
			return
		}
		read, err := reader.Read(chunk)
		if signal.Err() != nil {
			return
		}
		if float64(len(output)+read) > limit {
			part.Inflated = &Inflated{Kind: nextnode.InflatedTooLarge}
			return
		}
		output = append(output, chunk[:read]...)
		if err == io.EOF {
			break
		}
		if err != nil {
			part.Inflated = &Inflated{Kind: nextnode.InflatedCorrupt, Error: err}
			return
		}
	}
	if output == nil {
		output = []byte{}
	}
	part.Inflated = &Inflated{Kind: nextnode.InflatedBytes, Bytes: output}
}

// bytesReader is a flate.Reader over bytes.
type bytesReader struct {
	data []byte
	at   int
}

func (reader *bytesReader) Read(into []byte) (int, error) {
	if reader.at >= len(reader.data) {
		return 0, io.EOF
	}
	read := copy(into, reader.data[reader.at:])
	reader.at += read
	return read, nil
}

func (reader *bytesReader) ReadByte() (byte, error) {
	if reader.at >= len(reader.data) {
		return 0, io.EOF
	}
	reader.at++
	return reader.data[reader.at-1], nil
}

// sizeOf is a part's size on the wire: its UTF-8 bytes.
func sizeOf(part Part) int {
	switch typed := part.(type) {
	case Text:
		return len(typed)
	case Bytes:
		return len(typed)
	case *DeflatedPart:
		return len(typed.Bytes)
	}
	return 0
}

// tooLarge is the error of a part past a limit (size -1 while inflating: unknown).
func tooLarge(size int, limit float64, measure string) *data.ChordError {
	what := "a part"
	if size >= 0 {
		what = fmt.Sprintf("a part of %d bytes", size)
	}
	message := fmt.Sprintf("%s is over the %s limit of %s bytes", what, measure, formatLimit(limit))
	return data.NewChordError(data.ChordErrorInit{Code: data.CodeTooLarge, Reason: data.ReasonPart, Message: message}, nil)
}

// formatLimit prints a limit as JavaScript prints a number.
func formatLimit(limit float64) string {
	if math.IsInf(limit, 1) {
		return "Infinity"
	}
	return fmt.Sprintf("%.0f", limit)
}
