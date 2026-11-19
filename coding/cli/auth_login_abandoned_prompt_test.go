package cli

// pi: packages/coding-agent/src/cli/auth-terminal.ts (readSecret: finish() runs inside the abort listener, before anything else sees the abort)

import (
	"context"
	"slices"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/MichaelKinsy/PiG/ai"
)

// abandonedSecretTerminal is a terminal whose secret prompt hands the terminal back only when the test lets it: a prompt goroutine that the scheduler has not run
// since the abort. Its events are what reaches the terminal, in order.
type abandonedSecretTerminal struct {
	*scriptedTerminal
	prompting chan struct{}
	release   chan struct{}

	mu     sync.Mutex
	events []string
}

func newAbandonedSecretTerminal() *abandonedSecretTerminal {
	return &abandonedSecretTerminal{scriptedTerminal: newScriptedTerminal(), prompting: make(chan struct{}), release: make(chan struct{})}
}

func (t *abandonedSecretTerminal) record(event string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.events = append(t.events, event)
}

func (t *abandonedSecretTerminal) recorded() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.events)
}

// Secret is readSecret as the abandoned goroutine runs it: raw from the prompt on, the restore and the newline of finish() once ctx ends and the goroutine runs.
func (t *abandonedSecretTerminal) Secret(ctx context.Context, _ string) (string, error) {
	close(t.prompting)
	<-ctx.Done()
	<-t.release
	t.record("restore")
	t.record("\n")
	return "", errLoginCancelled
}

func (t *abandonedSecretTerminal) Error(line string) { t.record(line) }

// abandoningLogin is a model runtime's Login as ai.awaitModelsOperation runs it (ai/models_runtime.go:167-185): the login, and the prompt it asks, run on a goroutine of
// their own, and the call returns the moment ctx ends, without waiting for that goroutine (Pi's raceWithAbortSignal).
func abandoningLogin(ctx context.Context, _ string, _ ai.AuthType, interaction ai.AuthInteraction, _ ...ai.LoginOptions) (ai.Credential, error) {
	prompted := make(chan error, 1)
	go func() {
		_, err := interaction.Prompt(ctx, ai.AuthSecretPrompt{Message: "Enter Login Test API key"})
		prompted <- err
	}()
	select {
	case err := <-prompted:
		return ai.Credential{}, err
	case <-ctx.Done():
		return ai.Credential{}, context.Cause(ctx)
	}
}

// A login that ends while its secret prompt is up must not go on, print or end the command before that prompt has given the terminal back. The prompt runs on a
// goroutine that the runtime abandons when ctx ends, and its restore runs on that goroutine: under load the command exited first and left the terminal raw
// (TestLoginCommandsEndOnASignal, API key prompt, Ctrl+C: 3 of 200 under 48 busy loops, 326 of 400 with GOMAXPROCS=1 for pig). In Pi the restore runs in the abort listener,
// before the abort reaches anything else, so the newline of the prompt comes before "Login cancelled.".
func TestAuthLoginWaitsForAnAbandonedSecretPromptToGiveTheTerminalBack(t *testing.T) {
	runtime, _ := authTestRuntime(t, nil, authTestProvider(apiKeyOnly(), authLoginTestProviderID))
	override := loginRuntimeOverride{AuthLoginRuntime: runtime, login: abandoningLogin}
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		terminal := newAbandonedSecretTerminal()
		release := sync.OnceFunc(func() { close(terminal.release) })
		defer release()
		done := make(chan int, 1)
		go func() {
			done <- runAuthLoginForTest(ctx, []string{authLoginTestProviderID, "--api-key"}, override, terminal, nonexistentAgentDir)
		}()
		<-terminal.prompting
		cancel()
		// Every goroutine of the bubble now waits: the prompt for its turn to restore, the command for the prompt.
		synctest.Wait()
		select {
		case code := <-done:
			t.Fatalf("the login ended with code %d while its secret prompt still held the terminal raw; terminal events %q", code, terminal.recorded())
		default:
		}
		release()
		if code := <-done; code != 1 {
			t.Fatalf("code %d, want 1", code)
		}
		if got, want := terminal.recorded(), []string{"restore", "\n", "Login cancelled."}; !slices.Equal(got, want) {
			t.Fatalf("terminal events %q, want %q", got, want)
		}
	})
}
