package subprocess

import (
	"bytes"
	stdjson "encoding/json"
	"sync"
	"unicode/utf8"

	"github.com/MichaelKinsy/PiG/internal/jsonwire"
)

// responseFrame is the Envelope of a response frame the read loop decoded by itself (parseResponseFrame), with its payload and room for a short result. The frames of an event round trip are one per event and carry no more than a result, so they are reused: the dispatcher of the request that received one returns it with releaseResponse once it holds nothing of it, and one that is not returned is simply collected.
type responseFrame struct {
	env     Envelope
	payload ResponsePayload
	result  [64]byte
}

var responseFrames sync.Pool

// releaseResponse returns the envelope of a response to the pool when the read loop made it from a pooled frame. The caller no longer reads the envelope, its payload or its result: a short result lives in the frame.
func releaseResponse(resp *Envelope) {
	if resp == nil || resp.pooled == nil {
		return
	}
	frame := resp.pooled
	*frame = responseFrame{}
	responseFrames.Put(frame)
}

var (
	responseFramePrefix = []byte(`{"type":"response","id":"`)
	responseFrameMiddle = []byte(`","response":{`)
	responseFrameResult = []byte(`"result":`)
	responseFrameEnd    = []byte("}}")
)

// parseResponseFrame recognizes the frame that answers a request when it has exactly the shape the SDKs write for a handler that completed: a response envelope with a plain ASCII id and a response that holds nothing or only a result, as {"type":"response","id":ID,"response":{"result":RESULT}}. It returns the id and the raw result, nil for an empty response. Any other frame is not recognized, and decodeEnvelope reads it: a response that carries an error, other members, escapes in the id, or a result that is not valid JSON, that is padded with white space or that holds a lone surrogate escape.
func parseResponseFrame(data []byte) (id, result []byte, ok bool) {
	rest, found := bytes.CutPrefix(data, responseFramePrefix)
	if !found {
		return nil, nil, false
	}
	end := 0
	for end < len(rest) && rest[end] != '"' {
		if b := rest[end]; b < 0x20 || b == '\\' || b >= utf8.RuneSelf {
			return nil, nil, false
		}
		end++
	}
	if end == 0 || end == len(rest) {
		return nil, nil, false
	}
	id = rest[:end]
	rest, found = bytes.CutPrefix(rest[end:], responseFrameMiddle)
	if !found {
		return nil, nil, false
	}
	if bytes.Equal(rest, responseFrameEnd) {
		return id, nil, true
	}
	rest, found = bytes.CutPrefix(rest, responseFrameResult)
	if !found {
		return nil, nil, false
	}
	result, found = bytes.CutSuffix(rest, responseFrameEnd)
	// A valid JSON value followed by the two closing braces is the whole of the frame: the value cannot swallow a brace that belongs to a later member.
	if !found || len(result) == 0 || result[0] <= ' ' || result[len(result)-1] <= ' ' || !utf8.Valid(result) || !stdjson.Valid(result) || jsonwire.HasLoneSurrogateEscape(data) {
		return nil, nil, false
	}
	return id, result, true
}

// pooledResponse returns the envelope of a parsed response frame for the request named id, holding result.
func pooledResponse(id string, result []byte) *Envelope {
	frame, _ := responseFrames.Get().(*responseFrame)
	if frame == nil {
		frame = new(responseFrame)
	}
	frame.env = Envelope{Type: MsgResponse, ID: id, Response: &frame.payload, pooled: frame}
	switch {
	case len(result) == 0:
	case len(result) <= len(frame.result):
		frame.payload.Result = frame.result[:copy(frame.result[:], result)]
	default:
		frame.payload.Result = bytes.Clone(result)
	}
	return &frame.env
}

// internRequestID returns the string for id, the bytes of a response's id, without allocating when it is an ID of the connection's current chunk.
func (c *Conn) internRequestID(id []byte) string {
	if chunk := c.idChunk.Load(); chunk != nil && len(id) > 1 && id[0] == 'r' {
		var n uint64
		for _, b := range id[1:] {
			if b < '0' || b > '9' || n > (1<<60) {
				return string(id)
			}
			n = n*10 + uint64(b-'0')
		}
		if at := n - chunk.first; at < idChunkSize && chunk.ids[at] == string(id) {
			return chunk.ids[at]
		}
	}
	return string(id)
}
