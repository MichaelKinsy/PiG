package ai

import "io"

// newOpenAIRecordPath is parseSSE's record path before the builder: the stream decoder, the chunk decode and the response model.
// next answers false at the end of the stream or at a record the chunk decode does not read.
func newOpenAIRecordPath(reader io.Reader, configuredModel string) (next func() (oaiChunk, string, bool)) {
	decoder := newOpenAIStreamDecoder(reader, nil, nil)
	decoder.decodeChunks()
	return func() (oaiChunk, string, bool) {
		if !decoder.Next() {
			return oaiChunk{}, "", false
		}
		chunk, ok := decoder.Chunk()
		if !ok {
			return oaiChunk{}, "", false
		}
		return chunk, openAIChunkResponseModel(chunk.Model, configuredModel), true
	}
}
