package pipelinediff

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// capture is an HTTP server that records the first request body of each path prefix and answers 400, so neither implementation retries.
type capture struct {
	server *httptest.Server
	mu     sync.Mutex
	bodies map[string][]byte
	next   atomic.Int64
}

func newCapture(t *testing.T) *capture {
	c := &capture{bodies: map[string][]byte{}}
	c.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		prefix, _, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		c.mu.Lock()
		if _, ok := c.bodies[prefix]; !ok {
			c.bodies[prefix] = body
		}
		c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"capture","type":"invalid_request_error","code":"invalid_request"}}`))
	}))
	t.Cleanup(c.server.Close)
	return c
}

// slot reserves a base URL and the key its request body is stored under.
func (c *capture) slot(side string) (baseURL, key string) {
	key = fmt.Sprintf("%s%d", side, c.next.Add(1))
	return c.server.URL + "/" + key, key
}

func (c *capture) take(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	body, ok := c.bodies[key]
	delete(c.bodies, key)
	return body, ok
}

// piRoot is the installed Pi comparator pinned by extensions/sdk-ts.
func piRoot(t testing.TB) string {
	t.Helper()
	root := os.Getenv("PI_PACKAGE_ROOT")
	if root == "" {
		root = filepath.Join(testenv.ModuleRoot(t), "extensions", "sdk-ts", "node_modules", "@earendil-works", "pi-coding-agent")
	}
	if _, err := os.Stat(filepath.Join(root, "dist", "core", "messages.js")); err != nil {
		t.Fatalf("the exact Pi comparator is not installed at %s (run make parity-deps): %v", root, err)
	}
	return root
}

// piWorker is one Node process running pi-worker.mjs.
type piWorker struct {
	cmd   *exec.Cmd
	stdin io.WriteCloser
	out   *bufio.Reader
	id    int
}

func startPiWorker(t testing.TB) *piWorker {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required: %v", err)
	}
	cmd := exec.Command(node, filepath.Join(testenv.ModuleRoot(t), "test", "pipeline-diff", "pi-worker.mjs"), piRoot(t))
	cmd.Stderr = os.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	w := &piWorker{cmd: cmd, stdin: stdin, out: bufio.NewReaderSize(stdout, 1<<20)}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
	})
	return w
}

// runWithStatus sends one case through Pi's pipeline against baseURL. A non-nil error is a Pi failure before the request is built; streamError is the error message of a stream that ended in error, which is the transport's 400 when the request was sent.
func (w *piWorker) runWithStatus(c testCase, baseURL string) (streamError string, err error) {
	w.id++
	request := map[string]any{"id": w.id, "provider": c.Target.Provider, "modelId": c.Target.Model, "baseUrl": baseURL, "systemPrompt": c.SystemPrompt, "messages": c.Messages}
	line, err := json.Marshal(request)
	if err != nil {
		return "", err
	}
	if _, err := w.stdin.Write(append(line, '\n')); err != nil {
		return "", err
	}
	reply, err := w.out.ReadBytes('\n')
	if err != nil {
		return "", fmt.Errorf("pi worker: %w", err)
	}
	var parsed struct {
		ID           int    `json:"id"`
		Error        string `json:"error"`
		ErrorMessage string `json:"errorMessage"`
	}
	if err := json.Unmarshal(reply, &parsed); err != nil {
		return "", fmt.Errorf("pi worker reply %q: %w", reply, err)
	}
	if parsed.ID != w.id {
		return "", errors.New("pi worker reply out of order")
	}
	if parsed.Error != "" {
		return "", errors.New(parsed.Error)
	}
	return parsed.ErrorMessage, nil
}
