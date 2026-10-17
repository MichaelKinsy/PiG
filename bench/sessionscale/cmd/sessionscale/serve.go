// SPDX-License-Identifier: MIT

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// scripted is an OpenAI chat-completions endpoint that streams a short answer ending in a per-request marker. It records each request's size, message count and the hash of its non-system messages, so two runtimes resuming the same file can be checked to send the same context.
type scripted struct {
	mu       sync.Mutex
	requests []requestRecord
	// chunkDelay paces the streamed chunks (the soak); chunks is how many text chunks each answer has.
	chunkDelay time.Duration
	chunks     int
	// dumpDir, when set, receives each request body as request-<n>.json.
	dumpDir string
}

type requestRecord struct {
	N            int    `json:"n"`
	Bytes        int    `json:"bytes"`
	Messages     int    `json:"messages"`
	MessagesHash string `json:"messagesHash"`
	Start        int64  `json:"startUnixMs"`
	End          int64  `json:"endUnixMs"`
}

func newScripted() *scripted { return &scripted{chunks: 8} }

// marker is the text that ends answer n. It is sent as a paragraph of its own, so the terminal never wraps it.
func marker(n int) string { return fmt.Sprintf("sessionscale-done-%d", n) }

func (s *scripted) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		Messages []json.RawMessage `json:"messages"`
	}
	_ = json.Unmarshal(body, &req)
	// The hash covers the conversation, not the system prompt, which names the runtime's temporary home.
	h := sha256.New()
	for _, m := range req.Messages {
		var role struct {
			Role string `json:"role"`
		}
		if json.Unmarshal(m, &role) == nil && (role.Role == "system" || role.Role == "developer") {
			continue
		}
		h.Write(m)
		h.Write([]byte{'\n'})
	}
	s.mu.Lock()
	n := len(s.requests) + 1
	rec := requestRecord{N: n, Bytes: len(body), Messages: len(req.Messages), MessagesHash: hex.EncodeToString(h.Sum(nil))[:16], Start: time.Now().UnixMilli()}
	s.requests = append(s.requests, rec)
	delay, chunks, dump := s.chunkDelay, s.chunks, s.dumpDir
	s.mu.Unlock()
	if dump != "" {
		_ = os.WriteFile(filepath.Join(dump, fmt.Sprintf("request-%d.json", n)), body, 0o600)
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	send := func(v any) {
		data, _ := json.Marshal(v)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
	id := fmt.Sprintf("chatcmpl-%d", n)
	chunk := func(delta map[string]any, finish any) map[string]any {
		return map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "scripted",
			"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	}
	send(chunk(map[string]any{"role": "assistant", "content": ""}, nil))
	for i := range chunks {
		if delay > 0 {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(delay): //portlint:allow golifetime Go 1.23+ (go.mod: 1.26) collects an unreferenced time.After timer, so a per-chunk wait leaks nothing
			}
		}
		send(chunk(map[string]any{"content": fmt.Sprintf("Answer %d part %d: the change keeps the session bounded. ", n, i)}, nil))
	}
	send(chunk(map[string]any{"content": "\n\n" + marker(n)}, nil))
	send(chunk(map[string]any{}, "stop"))
	prompt := 1000 // fixed, so the measured prompts never trigger auto-compaction
	send(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": "scripted", "choices": []any{},
		"usage": map[string]any{"prompt_tokens": prompt, "completion_tokens": 40, "total_tokens": prompt + 40}})
	_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
	s.mu.Lock()
	s.requests[n-1].End = time.Now().UnixMilli()
	s.mu.Unlock()
}

func (s *scripted) records() []requestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]requestRecord(nil), s.requests...)
}

// start listens on addr and returns the base URL (ending in /v1).
func (s *scripted) start(addr string) (string, func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", nil, err
	}
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }() //portlint:allow golifetime owned by srv: the returned stop func calls srv.Close, which makes Serve return
	return "http://" + ln.Addr().String() + "/v1", func() { _ = srv.Close() }, nil
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", "127.0.0.1:0", "listen address")
	delay := fs.Duration("chunk-delay", 0, "delay between streamed chunks")
	_ = fs.Parse(args)
	s := newScripted()
	s.chunkDelay = *delay
	url, _, err := s.start(*addr)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintln(os.Stdout, url); err != nil {
		return err
	}
	select {}
}
