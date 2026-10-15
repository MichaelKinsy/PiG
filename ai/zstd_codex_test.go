package ai

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
)

// decodeZstdFrameForTest decodes a Codex request body frame.
func decodeZstdFrameForTest(frame []byte) ([]byte, error) {
	decoder, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	return decoder.DecodeAll(frame, nil)
}

func TestCompressCodexRequestBodyRoundTrips(t *testing.T) {
	for _, size := range []int{0, 1, 255, 256, 65_791, 65_792, 128*1024 + 7, 3 << 20} {
		input := bytes.Repeat([]byte("x"), size)
		decoded, err := decodeZstdFrameForTest(compressCodexRequestBody(input))
		if err != nil {
			t.Fatalf("size %d: %v", size, err)
		}
		if !bytes.Equal(decoded, input) {
			t.Fatalf("size %d: decoded payload differs", size)
		}
	}
}

// Requests to the Codex endpoint are compressed from several goroutines at once; each frame must decode to its own body.
func TestCompressCodexRequestBodyIsSafeForConcurrentRequests(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 16 {
		wg.Go(func() {
			body := bytes.Repeat([]byte{byte('a' + i)}, 10_000+i)
			decoded, err := decodeZstdFrameForTest(compressCodexRequestBody(body))
			if err != nil || !bytes.Equal(decoded, body) {
				t.Errorf("request %d: frame does not decode to its body (%v)", i, err)
			}
		})
	}
	wg.Wait()
}

func BenchmarkCompressCodexRequestBody(b *testing.B) {
	for _, c := range []struct {
		name string
		body []byte
	}{{"prose-400", codexProseBody(400)}, {"prose-4000", codexProseBody(4000)}} {
		b.Run(c.name, func(b *testing.B) {
			b.SetBytes(int64(len(c.body)))
			b.ReportAllocs()
			compressCodexRequestBody(c.body)
			for b.Loop() {
				compressCodexRequestBody(c.body)
			}
		})
	}
}

// codexSizedBody is the shape of a Codex request body: instructions, tool schemas and a long input of messages and tool outputs, as a session of the given number of turns sends it.
func codexSizedBody(turns int) []byte {
	var input []map[string]any
	for i := range turns {
		input = append(input,
			map[string]any{"type": "message", "role": "user", "content": []map[string]any{{"type": "input_text", "text": fmt.Sprintf("Turn %d: please read the file src/module_%d.go and explain what the retry logic does when the upstream returns 429.", i, i%17)}}},
			map[string]any{"type": "function_call", "call_id": fmt.Sprintf("call_%d", i), "name": "read", "arguments": fmt.Sprintf(`{"path":"src/module_%d.go","offset":%d,"limit":200}`, i%17, i*7)},
			map[string]any{"type": "function_call_output", "call_id": fmt.Sprintf("call_%d", i), "output": strings.Repeat(fmt.Sprintf("func handler%d(ctx context.Context) error {\n\tif err := retry(ctx, backoff); err != nil {\n\t\treturn fmt.Errorf(\"handler%d: %%w\", err)\n\t}\n\treturn nil\n}\n", i%17, i%17), 6)})
	}
	body, err := json.Marshal(map[string]any{
		"model": "gpt-5.1-codex", "store": false, "stream": true, "instructions": strings.Repeat("You are a coding agent. Follow the user's instructions exactly. ", 40),
		"tools": []map[string]any{{"type": "function", "name": "read", "description": "Read a file", "parameters": map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}, "offset": map[string]any{"type": "number"}, "limit": map[string]any{"type": "number"}}, "required": []string{"path"}}}},
		"input": input, "text": map[string]any{"verbosity": "medium"}, "include": []string{"reasoning.encrypted_content"}, "prompt_cache_key": "session-1", "tool_choice": "auto", "parallel_tool_calls": true,
	})
	if err != nil {
		panic(err)
	}
	return body
}

// codexProseBody is a Codex request whose input is varied text, words drawn from a fixed vocabulary in a fixed order, as turns of a real conversation are.
func codexProseBody(turns int) []byte {
	rng := rand.New(rand.NewSource(5))
	vocabulary := make([]string, 3000)
	for i := range vocabulary {
		word := make([]byte, 2+rng.Intn(9))
		for j := range word {
			word[j] = byte('a' + rng.Intn(26))
		}
		vocabulary[i] = string(word)
	}
	var input []map[string]any
	for i := range turns {
		var text strings.Builder
		for range 20 + rng.Intn(120) {
			text.WriteString(vocabulary[int(rng.ExpFloat64()*300)%len(vocabulary)])
			text.WriteByte(" \n,."[rng.Intn(4)])
		}
		input = append(input, map[string]any{"type": "message", "role": []string{"user", "assistant"}[i%2], "content": []map[string]any{{"type": "input_text", "text": text.String()}}, "id": fmt.Sprintf("msg_%08x", rng.Uint32())})
	}
	body, err := json.Marshal(map[string]any{"model": "gpt-5.1-codex", "store": false, "stream": true, "instructions": "You are a coding agent.", "input": input})
	if err != nil {
		panic(err)
	}
	return body
}
