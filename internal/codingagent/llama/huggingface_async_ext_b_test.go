package llama

// pi: packages/coding-agent/src/extensions/llama/huggingface.ts

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// huggingface.ts:72-80 request() hands fetch `AbortSignal.any([caller signal, 15 s timeout])` and awaits the response: aborting the
// caller's signal rejects search() and details() with the signal's reason and drops the connection, so the server's handler ends.
func TestHuggingFaceRequestsEndWithTheCallersAbortReason(t *testing.T) {
	for name, call := range map[string]func(context.Context, *HuggingFaceClient) error{
		"search": func(ctx context.Context, c *HuggingFaceClient) error { _, err := c.Search(ctx, "qwen"); return err },
		"details": func(ctx context.Context, c *HuggingFaceClient) error {
			_, err := c.Details(ctx, "owner/model-GGUF")
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			entered := make(chan struct{})
			dropped := make(chan struct{})
			url := listen(t, func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				<-r.Context().Done()
				close(dropped)
			})
			reason := errors.New("user cancelled the search")
			ctx, cancel := context.WithCancelCause(context.Background())
			result := make(chan error, 1)
			go func() { result <- call(ctx, NewHuggingFaceClient("", url)) }()
			<-entered
			cancel(reason)
			if err := <-result; !errors.Is(err, reason) {
				t.Fatalf("error = %v, want the abort reason", err)
			}
			<-dropped
		})
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// huggingface.ts:76-78 AbortSignal.timeout(15_000) bounds the request: a response that never arrives rejects at 15 s with the
// timeout error (not earlier), and a caller that also aborts keeps its own reason.
func TestHuggingFaceRequestTimesOutAtFifteenSeconds(t *testing.T) {
	transport := http.DefaultClient.Transport
	http.DefaultClient.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	t.Cleanup(func() { http.DefaultClient.Transport = transport })
	synctest.Test(t, func(t *testing.T) {
		result := make(chan error, 1)
		go func() {
			_, err := NewHuggingFaceClient("", "http://hf.invalid").Search(context.Background(), "qwen")
			result <- err
		}()
		time.Sleep(15*time.Second - time.Millisecond)
		synctest.Wait()
		select {
		case err := <-result:
			t.Fatalf("the request ended before 15 s: %v", err)
		default:
		}
		time.Sleep(time.Millisecond)
		synctest.Wait()
		select {
		case err := <-result:
			if !errors.As(err, new(timeoutError)) {
				t.Fatalf("error = %v, want the timeout error", err)
			}
		default:
			t.Fatal("the request did not end at 15 s")
		}
	})
}

// huggingface.ts:46-58 findHuggingFaceToken tries the token files in order and returns the first non-empty one; a file that is
// empty or missing falls through to the next.
func TestFindHuggingFaceTokenPrefersEarlierFilesAndSkipsEmptyOnes(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	write := func(path, content string) string {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	explicit := write(filepath.Join(dir, "explicit"), "  \n")
	hfHome := filepath.Join(dir, "hf")
	write(filepath.Join(hfHome, "token"), "from-hf-home")
	xdg := filepath.Join(dir, "xdg")
	write(filepath.Join(xdg, "huggingface", "token"), "from-xdg")
	env := map[string]string{"HF_TOKEN_PATH": explicit, "HF_HOME": hfHome, "XDG_CACHE_HOME": xdg}
	find := func() string { return FindHuggingFaceToken(func(name string) string { return env[name] }) }
	if got := find(); got != "from-hf-home" {
		t.Fatalf("token = %q, want the empty explicit file skipped for HF_HOME", got)
	}
	write(explicit, "from-explicit\n")
	if got := find(); got != "from-explicit" {
		t.Fatalf("token = %q, want the first file", got)
	}
}
