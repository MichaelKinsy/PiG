package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

// benchReplacementBuilder returns the process-fixed builder print mode uses, configured with one Node extension, and the startup build.
func benchReplacementBuilder(b *testing.B, extensions int) (*cliRuntimeBuilder, string, string) {
	b.Helper()
	home := b.TempDir()
	agentDir := filepath.Join(home, "agent")
	cwd := b.TempDir()
	b.Setenv("PIG_HOME", home)
	b.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	b.Setenv("PIG_TEST_FAUX", "1")
	b.Setenv("PIG_TEST_REPLACE_LOG", filepath.Join(home, "replace.log"))
	fixture, err := filepath.Abs(filepath.Join("testdata", "session-replace.mjs"))
	if err != nil {
		b.Fatal(err)
	}
	args := []string{"--model", "test-faux/faux-1", "--print"}
	for i := range extensions {
		copyPath := filepath.Join(home, "ext", string(rune('a'+i)), "session-replace.mjs")
		data, err := os.ReadFile(fixture)
		if err != nil {
			b.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(copyPath), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(copyPath, data, 0o644); err != nil {
			b.Fatal(err)
		}
		args = append(args, "-e", copyPath)
	}
	flags := parseFlags(args)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	builder := &cliRuntimeBuilder{
		mode: processAppMode(flags), flags: flags, agentDir: agentDir, launchCWD: cwd,
		settingsManager: settings, settingsDiagnostics: codingagent.CollectSettingsDiagnostics(settings),
		startupUIOptions: startupUIOptions(flags, false, cwd, agentDir, settings),
		trustStore:       codingagent.NewProjectTrustStore(agentDir), trustByCWD: map[string]bool{},
		stageExtensionSDKs: func() {},
	}
	return builder, cwd, agentDir
}

// BenchmarkSessionReplacementCycle measures one production replacement in print mode: teardown of the outgoing Session, the factory's rebuild of Services, Resources and the extension host for the destination cwd, the new Session, and the retirement of the outgoing host. It reports the phases the factory spends time in as custom metrics.
func BenchmarkSessionReplacementCycle(b *testing.B) {
	for _, extensions := range []int{1, 4} {
		b.Run(map[int]string{1: "1ext", 4: "4ext"}[extensions], func(b *testing.B) {
			builder, cwd, agentDir := benchReplacementBuilder(b, extensions)
			ctx := context.Background()
			var rebuildTotal time.Duration
			start := time.Now()
			build, err := builder.buildResources(ctx, cliBuildInput{CWD: cwd})
			if err != nil {
				b.Fatal(err)
			}
			manager, err := coding.NewInMemorySessionManager(cwd)
			if err != nil {
				b.Fatal(err)
			}
			if err := builder.buildSession(ctx, build, cliBuildInput{CWD: cwd, Manager: manager}); err != nil {
				b.Fatal(err)
			}
			b.Logf("startup build: %v", time.Since(start))
			host := builder.printHost(build, &printStartup{Manager: manager})
			factory := newCLISessionFactory(ctx, host.inputs(), host.state(), func(ctx context.Context, options coding.CreateAgentSessionRuntimeOptions) (cliSessionInputs, printSessionState, error) {
				t0 := time.Now()
				next, err := builder.rebuild(ctx, options)
				rebuildTotal += time.Since(t0)
				if err != nil {
					return cliSessionInputs{}, printSessionState{}, err
				}
				replacement := builder.printHost(next, nil)
				return replacement.inputs(), replacement.state(), nil
			})
			defer factory.Close()
			rt, err := coding.CreateAgentSessionRuntime(ctx, factory.Factory(), coding.CreateAgentSessionRuntimeOptions{CWD: cwd, AgentDir: agentDir, SessionManager: manager})
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = rt.Close() }()
			b.ResetTimer()
			for range b.N {
				result, err := rt.NewSession(ctx, &extension.NewSessionOptions{})
				if err != nil || result.Cancelled {
					b.Fatalf("new session: cancelled=%v err=%v", result.Cancelled, err)
				}
			}
			b.StopTimer()
			b.ReportMetric(float64(rebuildTotal.Milliseconds())/float64(b.N), "rebuild-ms/op")
		})
	}
}

// BenchmarkSessionReplacementPhases times the parts of one replacement build separately: buildResources (trust, services, resource discovery and the extension host load), buildSession (model, tools and system prompt) and the release that stops the extension host and closes Services. The sum is the factory's cost outside Session construction.
func BenchmarkSessionReplacementPhases(b *testing.B) {
	for _, extensions := range []int{1, 4} {
		b.Run(map[int]string{1: "1ext", 4: "4ext"}[extensions], func(b *testing.B) {
			builder, cwd, _ := benchReplacementBuilder(b, extensions)
			ctx := context.Background()
			manager, err := coding.NewInMemorySessionManager(cwd)
			if err != nil {
				b.Fatal(err)
			}
			var resources, session, release time.Duration
			for range b.N {
				in := cliBuildInput{CWD: cwd, Manager: manager}
				t0 := time.Now()
				build, err := builder.buildResources(ctx, in)
				if err != nil {
					b.Fatal(err)
				}
				t1 := time.Now()
				if err := builder.buildSession(ctx, build, in); err != nil {
					b.Fatal(err)
				}
				t2 := time.Now()
				if build.Host != nil {
					build.Host.Shutdown("benchmark")
				}
				build.Services.Close()
				t3 := time.Now()
				resources += t1.Sub(t0)
				session += t2.Sub(t1)
				release += t3.Sub(t2)
			}
			n := float64(b.N)
			b.ReportMetric(float64(resources.Microseconds())/1000/n, "buildResources-ms/op")
			b.ReportMetric(float64(session.Microseconds())/1000/n, "buildSession-ms/op")
			b.ReportMetric(float64(release.Microseconds())/1000/n, "release-ms/op")
		})
	}
}
