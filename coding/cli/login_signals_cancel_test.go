package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
)

// contextLoginProvider is a provider that, like every provider of PiG, implements LoginContext: its login waits for the command to end it.
type contextLoginProvider struct {
	callbackLoginProvider
	started chan struct{}
}

func (p contextLoginProvider) LoginContext(ctx context.Context, _ ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
	close(p.started)
	<-ctx.Done()
	return ai.OAuthCredentials{}, context.Cause(ctx)
}

// Every provider's login runs under the command's context: a signal that cancels it ends a login that is waiting for the user, whether the provider takes
// the context (every provider of PiG) or only has Login (loginWithContext abandons it). Before, the login ran under context.Background() and the
// command waited for a code nobody would enter. The command then prints nothing and writes no credential.
func TestLoginEndsWhenItsContextEnds(t *testing.T) {
	for _, tc := range []struct {
		name     string
		provider func(t *testing.T, started chan struct{}) ai.OAuthProviderInterface
	}{
		{"provider with LoginContext", func(t *testing.T, started chan struct{}) ai.OAuthProviderInterface {
			return contextLoginProvider{callbackLoginProvider{id: "context-login-probe"}, started}
		}},
		{"provider with Login only", func(t *testing.T, started chan struct{}) ai.OAuthProviderInterface {
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			return callbackLoginProvider{id: "plain-login-probe", login: func(ai.OAuthLoginCallbacks) (ai.OAuthCredentials, error) {
				close(started)
				<-release
				return ai.OAuthCredentials{Access: "never"}, nil
			}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			started := make(chan struct{})
			provider := tc.provider(t, started)
			ai.RegisterOAuthProvider(provider.ID(), provider)
			t.Cleanup(func() { ai.UnregisterOAuthProvider(provider.ID()) })
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()

			stdout, stderr, code := captureWithStdin(t, "", func() int {
				done := make(chan int, 1)
				go func() { done <- runLoginOptions(ctx, &loginCLIOptions{command: authLogin, provider: provider.ID()}) }()
				select {
				case <-started:
				case <-done:
					t.Error("the login ended before it waited")
					return -1
				}
				cancel()
				select {
				case code := <-done:
					return code
				case <-t.Context().Done():
					return -1
				}
			})
			if code != 1 || stdout != "" || stderr != "" {
				t.Fatalf("code %d, stdout %q, stderr %q; want 1 with nothing printed", code, stdout, stderr)
			}
			if _, err := os.Stat(filepath.Join(agentDir, "auth.json")); !os.IsNotExist(err) {
				t.Fatalf("a cancelled login created auth.json: %v", err)
			}
		})
	}
}

// The prompts of the command, which block on standard input, end with the context too: the provider menu of `pig login` and the menu of `pig logout`.
// Before, the read had no context, and a signal that cancelled the registry's context left the command waiting for a number.
func TestMenuPromptsEndWithTheContext(t *testing.T) {
	const seeded = `{"anthropic":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000},"github-copilot":{"type":"oauth","refresh":"r","access":"a","expires":4102444800000}}`
	for _, tc := range []struct {
		name    string
		command loginCommand
	}{{"login menu", authLogin}, {"logout menu", authLogout}} {
		t.Run(tc.name, func(t *testing.T) {
			agentDir := t.TempDir()
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			authPath := filepath.Join(agentDir, "auth.json")
			if err := os.WriteFile(authPath, []byte(seeded), 0o600); err != nil {
				t.Fatal(err)
			}
			stdinRead, stdinWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			stdoutRead, stdoutWrite, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			oldStdin, oldStdout := os.Stdin, os.Stdout
			os.Stdin, os.Stdout = stdinRead, stdoutWrite
			t.Cleanup(func() {
				os.Stdin, os.Stdout = oldStdin, oldStdout
				// The reader the cancelled prompt abandoned ends with its input.
				_ = stdinWrite.Close()
				_ = stdinRead.Close()
				_ = stdoutWrite.Close()
				_ = stdoutRead.Close()
			})
			prompted := make(chan struct{})
			go func() {
				var seen strings.Builder
				buf := make([]byte, 4096)
				for notified := false; ; {
					n, err := stdoutRead.Read(buf)
					seen.Write(buf[:n])
					if !notified && strings.Contains(seen.String(), "Enter number (1-") {
						notified = true
						close(prompted)
					}
					if err != nil {
						return
					}
				}
			}()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan int, 1)
			go func() { done <- runLoginOptions(ctx, &loginCLIOptions{command: tc.command}) }()
			select {
			case <-prompted:
			case <-t.Context().Done():
				t.Fatal("the menu was never shown")
			}
			cancel()
			select {
			case code := <-done:
				if code != 1 {
					t.Fatalf("code %d, want 1", code)
				}
			case <-t.Context().Done():
				t.Fatal("the command still waits for a number after its context ended")
			}
			after, err := os.ReadFile(authPath)
			if err != nil || string(after) != seeded {
				t.Fatalf("auth.json changed: %q, %v", after, err)
			}
		})
	}
}

// readLineContext keeps readLine's answer when the line arrives first.
func TestReadLineContextReturnsTheLine(t *testing.T) {
	got, err := readLineContext(t.Context(), strings.NewReader("2\n"))
	if err != nil || got != "2" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := readLineContext(t.Context(), io.NopCloser(strings.NewReader(""))); err != nil {
		t.Fatalf("end of input is an empty answer, as in readLine: %v", err)
	}
}
