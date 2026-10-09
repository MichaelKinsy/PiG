package durableagent

// pi: packages/coding-agent/src/experimental/vacation/harness-setup.ts

// pi: packages/coding-agent/src/experimental/durable/harness-setup.ts

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/durable"
	"github.com/MichaelKinsy/PiG/durable/env"
	"github.com/MichaelKinsy/PiG/durable/harness"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func newSettings(t *testing.T, settingsJSON string) (*codingagent.SettingsManager, string) {
	t.Helper()
	agentDir := filepath.Join(t.TempDir(), "agent")
	if err := os.MkdirAll(agentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if settingsJSON != "" {
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settingsJSON), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return codingagent.NewSettingsManager(t.TempDir(), agentDir), agentDir
}

// harness-setup.ts:25-47: createHarnessSettings maps pi's settings onto the Harness settings, read at every use.
func TestCreateHarnessSettings(t *testing.T) {
	t.Run("maps the defaults", func(t *testing.T) {
		manager, _ := newSettings(t, "")
		settings := CreateHarnessSettings(manager)()
		// stream: timeoutMs is the idle timeout (300000), maxRetryDelayMs 60000, no maxRetries.
		want := &durable.ConversationStreamOptions{TimeoutMs: new(300000), MaxRetryDelayMs: new(60000)}
		if got := settings.Stream; got == nil || *got.TimeoutMs != *want.TimeoutMs || *got.MaxRetryDelayMs != *want.MaxRetryDelayMs || got.MaxRetries != nil {
			t.Fatalf("stream = %+v, want timeout 300000, max retry delay 60000, no max retries", got)
		}
		retry := settings.Retry
		if retry == nil || *retry.Enabled != true || *retry.MaxRetries != 3 || *retry.BaseDelayMs != 2000 || *retry.MaxAgentDelayMs != ai.DefaultMaxAgentRetryDelayMs {
			t.Fatalf("retry = %+v", retry)
		}
		compaction := settings.Compaction
		if compaction == nil || *compaction.Enabled != true || *compaction.ReserveTokens != 16384 || *compaction.KeepRecentTokens != 20000 {
			t.Fatalf("compaction = %+v", compaction)
		}
		if settings.SteeringMode != durable.QueueOneAtATime || settings.FollowUpMode != durable.QueueOneAtATime {
			t.Fatalf("modes = %q, %q", settings.SteeringMode, settings.FollowUpMode)
		}
	})

	t.Run("an explicit provider timeout wins over the idle timeout, and maxRetries appears only when set", func(t *testing.T) {
		manager, _ := newSettings(t, `{"httpIdleTimeoutMs":1000,"retry":{"provider":{"timeoutMs":0,"maxRetries":0,"maxRetryDelayMs":0}}}`)
		stream := CreateHarnessSettings(manager)().Stream
		// TypeScript's `??` keeps an explicit 0 for every one of them.
		if stream == nil || *stream.TimeoutMs != 0 || stream.MaxRetries == nil || *stream.MaxRetries != 0 || *stream.MaxRetryDelayMs != 0 {
			t.Fatalf("stream = %+v, want timeout 0, maxRetries 0, maxRetryDelayMs 0", stream)
		}
	})

	t.Run("a disabled idle timeout becomes the largest timer delay", func(t *testing.T) {
		manager, _ := newSettings(t, `{"httpIdleTimeoutMs":0}`)
		if got := *CreateHarnessSettings(manager)().Stream.TimeoutMs; got != 2147483647 {
			t.Fatalf("timeoutMs = %d, want 2147483647", got)
		}
	})

	t.Run("reads the settings at every use", func(t *testing.T) {
		manager, _ := newSettings(t, "")
		read := CreateHarnessSettings(manager)
		if got := read().SteeringMode; got != durable.QueueOneAtATime {
			t.Fatalf("steering = %q", got)
		}
		if err := manager.SetSteeringMode("all"); err != nil {
			t.Fatal(err)
		}
		if err := manager.SetCompactionEnabled(false); err != nil {
			t.Fatal(err)
		}
		settings := read()
		if settings.SteeringMode != durable.QueueAll || settings.FollowUpMode != durable.QueueOneAtATime || *settings.Compaction.Enabled {
			t.Fatalf("steering %q, follow-up %q, compaction enabled %v; want all, one-at-a-time, false", settings.SteeringMode, settings.FollowUpMode, *settings.Compaction.Enabled)
		}
		if err := manager.SetFollowUpMode("all"); err != nil {
			t.Fatal(err)
		}
		if got := read().FollowUpMode; got != durable.QueueAll {
			t.Fatalf("follow-up = %q, want all", got)
		}
	})

	t.Run("an invalid compaction token setting fails the read as the getter throws", func(t *testing.T) {
		manager, _ := newSettings(t, `{"compaction":{"reserveTokens":-5}}`)
		defer func() {
			if recover() == nil {
				t.Fatal("expected the settings read to panic")
			}
		}()
		CreateHarnessSettings(manager)()
	})
}

// harness-setup.ts:16-19 and 83-96: one execution environment per directory, shared by every conversation in it.
func mustEnv(t *testing.T, envs *ExecutionEnvs, target harness.EnvTarget) env.ExecutionEnv {
	t.Helper()
	executionEnv, err := envs.Env(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	return executionEnv
}

func TestExecutionEnvs(t *testing.T) {
	defaultCwd, other := t.TempDir(), t.TempDir()
	envs := NewExecutionEnvs(defaultCwd)
	first := mustEnv(t, envs, harness.EnvTarget{})
	if first.Cwd() != defaultCwd {
		t.Fatalf("default environment cwd = %q, want %q", first.Cwd(), defaultCwd)
	}
	if again := mustEnv(t, envs, harness.EnvTarget{Cwd: &defaultCwd}); again != first {
		t.Fatal("the default directory and the explicit default share one environment")
	}
	second := mustEnv(t, envs, harness.EnvTarget{Cwd: &other})
	if second == first || second.Cwd() != other {
		t.Fatalf("a different directory needs its own environment, got %v", second.Cwd())
	}
	if mustEnv(t, envs, harness.EnvTarget{Cwd: &other}) != second {
		t.Fatal("one environment per directory")
	}
	// cleanup() clears the map after cleaning each environment, so a later request builds a new one.
	if err := envs.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := mustEnv(t, envs, harness.EnvTarget{}); after == first {
		t.Fatal("cleanup drops the cached environments")
	}
}

// harness-setup.ts:16-19: pi's HTTP setup is the proxy setting and the idle timeout.
func TestConfigureHarnessHTTP(t *testing.T) {
	// Setenv registers the restore of each variable; the proxy setting applies only to an absent one.
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY"} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	previous := ai.ConfiguredHTTPIdleTimeoutMs()
	t.Cleanup(func() { _ = ai.ConfigureHTTPDispatcher(previous) })
	manager, _ := newSettings(t, `{"httpProxy":"http://proxy.invalid:8080","httpIdleTimeoutMs":12345}`)
	if err := ConfigureHarnessHTTP(manager); err != nil {
		t.Fatal(err)
	}
	if got := ai.ConfiguredHTTPIdleTimeoutMs(); got != 12345 {
		t.Fatalf("idle timeout = %d, want 12345", got)
	}
	if os.Getenv("HTTP_PROXY") != "http://proxy.invalid:8080" || os.Getenv("HTTPS_PROXY") != "http://proxy.invalid:8080" {
		t.Fatalf("proxy env = %q, %q", os.Getenv("HTTP_PROXY"), os.Getenv("HTTPS_PROXY"))
	}
}

func TestConfigureHarnessHTTPRejectsAnInvalidIdleTimeout(t *testing.T) {
	manager, _ := newSettings(t, `{"httpIdleTimeoutMs":"soon"}`)
	if err := ConfigureHarnessHTTP(manager); err == nil {
		t.Fatal("an unparseable httpIdleTimeoutMs must fail as getHttpIdleTimeoutMs throws")
	}
}
