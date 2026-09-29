package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() (returnErr error) {
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
		w.Header().Set("Connection", "close")
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"response\",\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"response\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer backend.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Go(func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		time.Sleep(400 * time.Millisecond)
		upstream, err := net.Dial("tcp", backend.Listener.Addr().String())
		if err != nil {
			return
		}
		defer func() { _ = upstream.Close() }()
		var copies sync.WaitGroup
		copies.Go(func() { _, _ = io.Copy(upstream, conn); _ = upstream.Close() })
		copies.Go(func() { _, _ = io.Copy(conn, upstream); _ = conn.Close() })
		copies.Wait()
	})
	defer func() { _ = listener.Close(); workers.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	baseURL := "https://" + listener.Addr().String() + "/v1"
	if len(os.Args) > 1 && os.Args[1] == "--pi" {
		directory, err := os.MkdirTemp("", "pig-connect-ca-")
		if err != nil {
			return err
		}
		defer func() { returnErr = errors.Join(returnErr, os.RemoveAll(directory)) }()
		ca := filepath.Join(directory, "ca.pem")
		if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: backend.Certificate().Raw}), 0600); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, "node", "test/parity/testdata/http-connect/pi.mjs", baseURL)
		command.Env = append(os.Environ(), "NODE_EXTRA_CA_CERTS="+ca, "HTTP_PROXY=", "HTTPS_PROXY=", "ALL_PROXY=", "http_proxy=", "https_proxy=", "all_proxy=")
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		return command.Run()
	}
	if err := ai.ConfigureHTTPDispatcher(100); err != nil {
		return err
	}
	// The hermetic fixture trusts only its generated loopback server via the existing explicit TLS opt-in; certificate verification is asserted separately in the transport unit tests.
	provider := ai.NewOpenAIProvider(ai.OpenAIConfig{APIKey: "test", Model: "gpt-test", ProviderID: "openai", BaseURL: baseURL, Insecure: true})
	stream, err := provider.Stream(ctx, ai.NormalizeContext(ai.Context{Messages: []ai.Message{ai.UserMessage{Content: ai.UserText("hi")}}}), ai.StreamOptions{})
	if err != nil {
		return err
	}
	result := stream.Result()
	if result.StopReason == ai.StopReasonError {
		return errors.New(result.ErrorMessage)
	}
	text := ""
	for _, block := range result.Content {
		if content, ok := block.(ai.TextContent); ok {
			text += content.Text
		}
	}
	// Close the HTTP connection explicitly so the relay's joined workers cannot outlive this probe.
	cancel()
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"reason": result.StopReason, "text": text})
}
