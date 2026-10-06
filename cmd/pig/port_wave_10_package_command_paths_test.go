package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/coding"
	"github.com/MichaelKinsy/PiG/coding/extension"
	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

func TestPortWave10PackageCommandPaths(t *testing.T) {
	// upstream: packages/coding-agent/test/package-command-paths.test.ts:190
	t.Run("should persist global relative local package paths relative to settings.json", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		relativePkgDir := filepath.Join(f.projectDir, "packages", "local-package")
		require.NoError(t, os.MkdirAll(relativePkgDir, 0o755))

		capturePackageCommand(t, "install", "./packages/local-package")

		packages := readPackageCommandPackages(t, filepath.Join(f.agentDir, "settings.json"))
		require.Len(t, packages, 1)
		assert.Equal(t, packageCommandRealpath(t, relativePkgDir), packageCommandRealpath(t, filepath.Join(f.agentDir, packages[0])))
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:204
	t.Run("should remove local packages using a path with a trailing slash", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		capturePackageCommand(t, "install", f.packageDir+"/")

		settingsPath := filepath.Join(f.agentDir, "settings.json")
		require.Len(t, readPackageCommandPackages(t, settingsPath), 1)

		capturePackageCommand(t, "remove", f.packageDir+"/")

		assert.Empty(t, readPackageCommandPackages(t, settingsPath))
	})

	for _, tc := range []struct {
		name         string
		saved        *bool
		defaultTrust string
		args         []string
		wantProject  bool
	}{
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:217
		{name: "skips untrusted project package settings", args: []string{"list"}},
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:233
		{name: "uses remembered project trust for list", saved: new(true), args: []string{"list"}, wantProject: true},
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:252
		{name: "overrides remembered trust for list with --no-approve", saved: new(true), args: []string{"list", "--no-approve"}},
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:270
		{name: "approves project trust for list with --approve", args: []string{"list", "--approve"}, wantProject: true},
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:288
		{name: "uses default project trust for list", defaultTrust: "always", args: []string{"list"}, wantProject: true},
		// upstream: packages/coding-agent/test/package-command-paths.test.ts:396
		{name: "lets trust.json override default project trust", saved: new(false), defaultTrust: "always", args: []string{"list"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageCommandPathsFixture(t)
			writeStartupFixtureFile(t, filepath.Join(f.projectDir, ".pi", "settings.json"), `{"packages":["npm:@project/pkg"]}`)
			if tc.defaultTrust != "" {
				writePackageCommandSettings(t, filepath.Join(f.agentDir, "settings.json"), map[string]string{"defaultProjectTrust": tc.defaultTrust})
			}
			if tc.saved != nil {
				require.NoError(t, codingagent.NewProjectTrustStore(f.agentDir).Set(f.projectDir, tc.saved))
			}

			stdout, _, code := capturePackageCommand(t, tc.args...)

			if tc.wantProject {
				assert.Contains(t, stdout, "Project packages:")
				assert.Contains(t, stdout, "npm:@project/pkg")
				assert.NotContains(t, stdout, "No packages installed.")
			} else {
				assert.Contains(t, stdout, "No packages installed.")
				assert.NotContains(t, stdout, "Project packages:")
			}
			assert.Equal(t, 0, code)
		})
	}

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:307
	t.Run("uses project_trust extensions for package commands", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		writeStartupFixtureFile(t, filepath.Join(f.projectDir, ".pi", "settings.json"), `{"packages":["npm:@project/pkg"]}`)
		registered, called := false, false
		options := packageCommandRuntimeOptions{extensionFactories: []func() (extension.Extension, error){func() (extension.Extension, error) {
			inline := extension.Extension{Path: "<inline>", ResolvedPath: "<inline>"}
			inline.AddEventHandler("project_trust", 1, func(...any) (any, error) {
				called = true
				return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
			})
			registered = true
			return inline, nil
		}}}
		stdout, _, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"list"}, options) })
		assert.True(t, registered)
		assert.True(t, called)
		assert.Contains(t, stdout, "Project packages:")
		assert.Contains(t, stdout, "npm:@project/pkg")
		assert.NotContains(t, stdout, "No packages installed.")
		assert.Equal(t, 0, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:333
	t.Run("does not prompt or ask extensions for project trust during update", func(t *testing.T) {
		node := nodeExecutableForPackageCommand(t)
		script, err := os.ReadFile(filepath.Join("testdata", "port-wave-10", "record-argv.cjs"))
		require.NoError(t, err)
		f := newPackageCommandPathsFixture(t)
		fakeNpmPath := filepath.Join(f.root, "fake-project-npm.cjs")
		recordPath := filepath.Join(f.root, "project-update.json")
		writeStartupFixtureFile(t, fakeNpmPath, string(script))
		t.Setenv("WAVE10_NPM_RECORD", recordPath)
		writeStartupFixtureFile(t, filepath.Join(f.agentDir, "settings.json"), `{"defaultProjectTrust":"always"}`)
		writePackageCommandSettings(t, filepath.Join(f.projectDir, ".pi", "settings.json"), map[string]any{
			"packages": []string{"npm:fake-package"}, "npmCommand": []string{node, fakeNpmPath},
		})
		called := false
		options := packageCommandRuntimeOptions{extensionFactories: []func() (extension.Extension, error){func() (extension.Extension, error) {
			inline := extension.Extension{Path: "<inline>", ResolvedPath: "<inline>"}
			inline.AddEventHandler("project_trust", 1, func(...any) (any, error) {
				called = true
				return extension.ProjectTrustEventResult{Trusted: extension.ProjectTrustYes}, nil
			})
			return inline, nil
		}}}
		_, _, code := captureStdoutStderr(t, func() int { return runPackageCommand([]string{"update", "--extensions"}, options) })
		assert.False(t, called)
		_, err = os.Stat(recordPath)
		assert.ErrorIs(t, err, os.ErrNotExist)
		assert.Equal(t, 0, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:371
	t.Run("uses saved project trust during update", func(t *testing.T) {
		node := nodeExecutableForPackageCommand(t)
		script, err := os.ReadFile(filepath.Join("testdata", "port-wave-10", "record-argv.cjs"))
		require.NoError(t, err)
		f := newPackageCommandPathsFixture(t)
		fakeNpmPath := filepath.Join(f.root, "fake-trusted-project-npm.cjs")
		recordPath := filepath.Join(f.root, "trusted-project-update.json")
		writeStartupFixtureFile(t, fakeNpmPath, string(script))
		t.Setenv("WAVE10_NPM_RECORD", recordPath)
		writePackageCommandSettings(t, filepath.Join(f.projectDir, ".pi", "settings.json"), map[string]any{
			"packages": []string{"npm:fake-package"}, "npmCommand": []string{node, fakeNpmPath},
		})
		require.NoError(t, codingagent.NewProjectTrustStore(f.agentDir).Set(f.projectDir, new(true)))

		_, _, code := capturePackageCommand(t, "update", "--extensions")

		_, err = os.Stat(recordPath)
		assert.NoError(t, err, "project npm command must create its record")
		assert.Equal(t, 0, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:415
	t.Run("blocks local package changes when project is untrusted", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		writeStartupFixtureFile(t, filepath.Join(f.projectDir, ".pi", "settings.json"), "{}")

		_, stderr, code := capturePackageCommand(t, "install", "-l", "./local-package")

		assert.Contains(t, stderr, "Project is not trusted. Use --approve to modify local package config.")
		assert.Equal(t, 1, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:431
	t.Run("allows local package install to initialize fresh project settings", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)

		_, _, code := capturePackageCommand(t, "install", "-l", f.packageDir)

		packages := readPackageCommandPackages(t, filepath.Join(f.projectDir, ".pi", "settings.json"))
		require.Len(t, packages, 1)
		assert.Equal(t, packageCommandRealpath(t, f.packageDir), packageCommandRealpath(t, filepath.Join(f.projectDir, ".pi", packages[0])))
		assert.Equal(t, 0, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:442
	t.Run("shows install subcommand help", func(t *testing.T) {
		newPackageCommandPathsFixture(t)

		stdout, stderr, code := capturePackageCommand(t, "install", "--help")

		assert.Contains(t, stdout, "Usage:")
		// D2 changes only the application name in the upstream usage assertion.
		assert.Contains(t, stdout, "pig install <source> [-l]")
		assert.Empty(t, stderr)
		assert.Equal(t, 0, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:460
	t.Run("refreshes only model catalogs with update --models", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		original := createPackageModelRuntime
		t.Cleanup(func() { createPackageModelRuntime = original })
		var calls []string
		var creationContext context.Context
		createPackageModelRuntime = func(ctx context.Context, options coding.CreateModelRuntimeOptions) (packageModelRuntime, error) {
			calls = append(calls, "create")
			creationContext = ctx
			assert.Equal(t, filepath.Join(f.agentDir, "auth.json"), options.AuthPath)
			if !assert.NotNil(t, options.ModelsPath) || !assert.NotNil(t, *options.ModelsPath) {
				return nil, errors.New("missing model path")
			}
			assert.Equal(t, filepath.Join(f.agentDir, "models.json"), **options.ModelsPath)
			assert.False(t, options.AllowModelNetwork)
			assert.NotNil(t, ctx.Done())
			assert.NoError(t, ctx.Err())
			return packageRefreshSpy{refresh: func(refreshContext context.Context, opts ai.ModelsRefreshOptions) ai.ModelsRefreshResult {
				calls = append(calls, "refresh")
				assert.Equal(t, ctx, refreshContext)
				assert.Equal(t, new(true), opts.AllowNetwork)
				assert.Equal(t, new(true), opts.Force)
				assert.NoError(t, refreshContext.Err())
				return ai.ModelsRefreshResult{Errors: map[string]error{}}
			}}, nil
		}

		stdout, stderr, code := capturePackageCommand(t, "update", "--models")

		assert.Equal(t, []string{"create", "refresh"}, calls)
		assert.Contains(t, stdout, "Model catalogs refreshed")
		assert.Empty(t, stderr)
		assert.Equal(t, 0, code)
		require.NotNil(t, creationContext)
		assert.ErrorIs(t, creationContext.Err(), context.Canceled)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:484
	t.Run("rejects update --models combined with another update target", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		original := createPackageModelRuntime
		t.Cleanup(func() { createPackageModelRuntime = original })
		created := false
		createPackageModelRuntime = func(context.Context, coding.CreateModelRuntimeOptions) (packageModelRuntime, error) {
			created = true
			return nil, errors.New("unexpected model runtime creation")
		}

		_, stderr, code := capturePackageCommand(t, "update", "--models", "--self")

		assert.False(t, created)
		assert.Contains(t, stderr, "--models cannot be combined with --self")
		assert.Equal(t, 1, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:497
	t.Run("cycles project package overrides in config local mode", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		writeStartupFixtureFile(t, filepath.Join(f.agentDir, "settings.json"), `{"packages":["npm:pi-tools"]}`)
		// Materialize the single resolved resource supplied by upstream's extensionPaths helper so the production selector constructor owns callback wiring.
		packageRoot := filepath.Join(f.agentDir, "npm", "node_modules", "pi-tools")
		// The fixture materializes an already-resolved npm resource, which requires a version in its installed manifest (package-manager.ts:1473-1478).
		writeStartupFixtureFile(t, filepath.Join(packageRoot, "package.json"), `{"name":"pi-tools","version":"1.0.0","pi":{"extensions":["extensions/bar.ts"]}}`)
		writeStartupFixtureFile(t, filepath.Join(packageRoot, "extensions", "bar.ts"), "export default function (pi) {}\n")
		global := codingagent.NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, false)
		settings := codingagent.NewSettingsManagerWithProjectTrust(f.projectDir, f.agentDir, true)
		selector, err := newScopedConfigSelector(f.projectDir, f.agentDir, global, settings, true, true, nil)
		require.NoError(t, err)
		selector.SetTerminalRows(24)

		// Pi's getPackageResourcePattern is path.relative, which spells the pattern with the platform separator.
		encoded, err := json.Marshal(filepath.Join("extensions", "bar.ts"))
		require.NoError(t, err)
		pattern := string(encoded[1 : len(encoded)-1])
		for _, want := range []string{
			`[{"source":"npm:pi-tools","autoload":false,"extensions":["-` + pattern + `"]}]`,
			`[{"source":"npm:pi-tools","autoload":false,"extensions":["+` + pattern + `"]}]`,
			`[]`,
		} {
			selector.HandleInput(" ")
			got, err := json.Marshal(settings.GetProjectSettings().Packages)
			require.NoError(t, err)
			assert.JSONEq(t, want, string(got))
		}
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:528
	t.Run("shows a friendly error for unknown install options", func(t *testing.T) {
		newPackageCommandPathsFixture(t)

		_, stderr, code := capturePackageCommand(t, "install", "--unknown")

		assert.Contains(t, stderr, `Unknown option --unknown for "install".`)
		// D2 changes only the application name, not the option/usage contract.
		assert.Contains(t, stderr, `Use "pig --help" or "pig install <source> [-l] [--approve|--no-approve]".`)
		assert.Equal(t, 1, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:543
	t.Run("shows a friendly error for missing install source", func(t *testing.T) {
		newPackageCommandPathsFixture(t)

		_, stderr, code := capturePackageCommand(t, "install")

		assert.Contains(t, stderr, "Missing install source.")
		// D2 changes only the application name in the upstream usage assertion.
		assert.Contains(t, stderr, "Usage: pig install <source> [-l]")
		assert.NotContains(t, stderr, "at ")
		assert.Equal(t, 1, code)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:952
	t.Run("suggests the configured source when update input omits the npm prefix", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		settingsPath := filepath.Join(f.agentDir, "settings.json")
		writeStartupFixtureFile(t, settingsPath, `{"packages":["npm:pi-formatter"]}`)

		stdout, stderr, code := capturePackageCommand(t, "update", "pi-formatter")

		assert.Contains(t, stderr, "Did you mean npm:pi-formatter?")
		assert.NotContains(t, stdout, "Updated pi-formatter")
		assert.Equal(t, 1, code)
		assert.Contains(t, readPackageCommandPackages(t, settingsPath), "npm:pi-formatter")
	})
}

// Constructor ownership supporting package-command-paths.test.ts:460 and model-runtime.ts:173-226.
func TestPortWave10ModelRuntimeCreation(t *testing.T) {
	t.Run("explicit model path is loaded and retained across refresh", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		path := filepath.Join(f.root, "configuration", "custom.json")
		authPath := filepath.Join(f.root, "credentials", "selected.json")
		writeStartupFixtureFile(t, authPath, `{"custom":{"type":"api_key","key":"selected-key"}}`)
		writeStartupFixtureFile(t, path, `{"providers":{"custom":{"api":"openai-completions","baseUrl":"https://custom.invalid","models":[{"id":"first"}]}}}`)
		writeStartupFixtureFile(t, filepath.Join(f.agentDir, "models.json"), `{"providers":{"wrong":{"api":"openai-completions","baseUrl":"https://wrong.invalid","models":[{"id":"wrong"}]}}}`)
		runtime, err := coding.CreateModelRuntime(t.Context(), coding.CreateModelRuntimeOptions{AuthPath: authPath, ModelsPath: new(&path)})
		require.NoError(t, err)
		require.NotNil(t, runtime.GetModel("custom", "first"))
		assert.Nil(t, runtime.GetModel("wrong", "wrong"))
		check, err := runtime.CheckAuth(t.Context(), "custom")
		require.NoError(t, err)
		require.NotNil(t, check)
		assert.Equal(t, ai.CredentialAPIKey, check.Type)
		writeStartupFixtureFile(t, path, `{"providers":{"custom":{"api":"openai-completions","baseUrl":"https://custom.invalid","models":[{"id":"second"}]}}}`)
		result := runtime.Refresh(t.Context(), ai.ModelsRefreshOptions{AllowNetwork: new(false)})
		assert.False(t, result.Aborted)
		assert.Empty(t, result.Errors)
		assert.Nil(t, runtime.GetModel("custom", "first"))
		assert.NotNil(t, runtime.GetModel("custom", "second"))
	})

	t.Run("null model path disables the default file and refreshOnCreate false does not read the store", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		writeStartupFixtureFile(t, filepath.Join(f.agentDir, "models.json"), `{"providers":{"wrong":{"api":"openai-completions","baseUrl":"https://wrong.invalid","models":[{"id":"wrong"}]}}}`)
		var reads atomic.Int32
		store := initialCatalogStore{InMemoryModelsStore: ai.NewInMemoryModelsStore(), read: func(context.Context, string) (*ai.ModelsStoreEntry, error) {
			reads.Add(1)
			return nil, nil
		}}
		runtime, err := coding.CreateModelRuntime(t.Context(), coding.CreateModelRuntimeOptions{ModelsPath: new((*string)(nil)), ModelsStore: store, RefreshOnCreate: new(false)})
		require.NoError(t, err)
		assert.Nil(t, runtime.GetModel("wrong", "wrong"))
		assert.Zero(t, reads.Load())
	})

	t.Run("initial cache restoration passes caller cancellation to every provider read", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		// Pi restores the cache of every refreshable provider concurrently (models.ts:562-576, read at :540): radius through its own provider and every other built-in through withRemoteCatalog (model-runtime.ts:228-231). PiG reads radius in radius_models.go and the others through the remote catalog collection, so the test cancels only once both paths are reading. Pi's refresh races the reads against the signal (models.ts:586,605), so the constructor need not join them.
		callerCause := errors.New("caller cancelled the initial refresh")
		announced := make(chan string)
		var released sync.WaitGroup
		var causesMu sync.Mutex
		var causes []error
		store := initialCatalogStore{InMemoryModelsStore: ai.NewInMemoryModelsStore(), read: func(ctx context.Context, provider string) (*ai.ModelsStoreEntry, error) {
			select {
			case announced <- provider:
				// The test counted this read before it cancelled and closes the runtime only after the read returned, so only the caller's cancellation can release it.
				defer released.Done()
				<-ctx.Done()
				causesMu.Lock()
				causes = append(causes, context.Cause(ctx))
				causesMu.Unlock()
			case <-ctx.Done():
			}
			return nil, context.Cause(ctx)
		}}
		ctx, cancel := context.WithCancelCause(t.Context())
		defer cancel(nil)
		type created struct {
			modelRuntime *coding.ModelRuntime
			err          error
		}
		result := make(chan created, 1)
		go func() {
			modelRuntime, err := coding.CreateModelRuntime(ctx, coding.CreateModelRuntimeOptions{ModelsStore: store})
			result <- created{modelRuntime, err}
		}()
		var radius, remote bool
		for !radius || !remote {
			select {
			case provider := <-announced:
				released.Add(1)
				if provider == codingagent.RadiusProviderID {
					radius = true
				} else {
					remote = true
				}
			case outcome := <-result:
				t.Fatalf("constructor returned before the radius and remote catalog reads started: radius=%v remote=%v err=%v", radius, remote, outcome.err)
			}
		}
		cancel(callerCause)
		outcome := <-result
		require.NoError(t, outcome.err)
		t.Cleanup(outcome.modelRuntime.Close)
		released.Wait()
		require.NotEmpty(t, causes)
		for _, cause := range causes {
			assert.ErrorIs(t, cause, callerCause)
		}
	})

	for _, tc := range []struct {
		name                        string
		allowNetwork, offline, skip bool
		requests                    int32
	}{
		{name: "initial refresh is cache-only by default"},
		{name: "initial network refresh is explicit", allowNetwork: true, requests: 1},
		{name: "offline blocks initial network refresh", allowNetwork: true, offline: true},
		{name: "refreshOnCreate false skips network and cache restoration", allowNetwork: true, skip: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPackageCommandPathsFixture(t)
			if !tc.offline {
				require.NoError(t, os.Unsetenv("PI_OFFLINE"))
			}
			t.Setenv("RADIUS_API_KEY", "")
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, "/v1/config", r.URL.Path)
				assert.Equal(t, "Bearer selected-key", r.Header.Get("Authorization"))
				_, _ = w.Write([]byte(`{"baseUrl":"https://gateway.invalid/v1","models":[{"id":"remote","name":"Remote","reasoning":false,"input":["text"],"cost":{"input":1,"output":2,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":16384}]}`))
			}))
			t.Cleanup(server.Close)
			writePackageCommandSettings(t, filepath.Join(f.agentDir, "models.json"), map[string]any{"providers": map[string]any{"gateway": map[string]any{"oauth": "radius", "baseUrl": server.URL + "/v1"}}})
			writeStartupFixtureFile(t, filepath.Join(f.agentDir, "auth.json"), `{"gateway":{"type":"api_key","key":"selected-key"}}`)
			runtime, err := coding.CreateModelRuntime(t.Context(), coding.CreateModelRuntimeOptions{AllowModelNetwork: tc.allowNetwork, RefreshOnCreate: new(!tc.skip)})
			require.NoError(t, err)
			assert.Equal(t, tc.requests, requests.Load())
			if tc.requests != 0 {
				assert.NotNil(t, runtime.GetModel("gateway", "remote"))
				_, err := os.Stat(filepath.Join(f.agentDir, "models-store.json"))
				assert.NoError(t, err)
			}
		})
	}

	t.Run("initial network refresh timeout cancels cache restoration", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		require.NoError(t, os.Unsetenv("PI_OFFLINE"))
		synctest.Test(t, func(t *testing.T) {
			// Every provider that refreshes reads the store from its own goroutine.
			var causeMu sync.Mutex
			var cause error
			store := initialCatalogStore{InMemoryModelsStore: ai.NewInMemoryModelsStore(), read: func(ctx context.Context, _ string) (*ai.ModelsStoreEntry, error) {
				<-ctx.Done()
				causeMu.Lock()
				defer causeMu.Unlock()
				cause = context.Cause(ctx)
				return nil, cause
			}}
			runtime, err := coding.CreateModelRuntime(t.Context(), coding.CreateModelRuntimeOptions{ModelsStore: store, AllowModelNetwork: true, ModelRefreshTimeoutMs: new(1)})
			require.NoError(t, err)
			assert.NotNil(t, runtime)
			causeMu.Lock()
			defer causeMu.Unlock()
			assert.ErrorIs(t, cause, context.DeadlineExceeded)
		})
	})
}

// D39 selects the native signed-manifest/receipt contract instead of Pi's npm-managed release layout.
func TestPortWave10NativeSelfUpdate(t *testing.T) {
	// upstream: packages/coding-agent/test/package-command-paths.test.ts:559
	t.Run("allows explicit self-update checks when automatic version checks are disabled", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		t.Setenv("PI_SKIP_VERSION_CHECK", "1")
		t.Setenv("PIG_SKIP_VERSION_CHECK", "1")
		var requests atomic.Int32
		server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = fmt.Fprintf(w, `{"version":%q,"packageName":"@pi-in-go/pig","binaries":{}}`, selfUpdateVersion())
		}))
		t.Cleanup(server.Close)
		t.Setenv("PIG_UPDATE_URL", server.URL)

		stdout, stderr, code := capturePackageCommand(t, "update", "--self")

		assert.Equal(t, int32(1), requests.Load())
		assert.Equal(t, 0, code)
		assert.Empty(t, stdout)
		assert.Equal(t, "pig "+selfUpdateVersion()+" is up to date.\n", stderr)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:585
	t.Run("retries a transient self-update version check", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		server := upToDateManifest(t)
		t.Cleanup(server.Close)
		t.Setenv("PIG_UPDATE_URL", server.URL)
		original := http.DefaultTransport
		t.Cleanup(func() { http.DefaultTransport = original })
		var attempts atomic.Int32
		http.DefaultTransport = packageUpdateTransport(func(request *http.Request) (*http.Response, error) {
			if attempts.Add(1) <= 2 {
				return nil, errors.New("fetch failed")
			}
			return original.RoundTrip(request)
		})

		_, stderr, code := capturePackageCommand(t, "update", "--self")

		assert.Equal(t, int32(3), attempts.Load())
		assert.Equal(t, 0, code)
		assert.Equal(t, "pig "+selfUpdateVersion()+" is up to date.\n", stderr)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:638
	t.Run("rejects a concurrent managed update", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		exe := filepath.Join(f.root, "pig")
		writeStartupFixtureFile(t, exe, "old")
		payload := []byte("new native release")
		digest := sha256.Sum256(payload)
		started, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		var downloads atomic.Int32
		binaryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if downloads.Add(1) == 1 {
				close(started)
				select {
				case <-release:
				case <-r.Context().Done():
					return
				}
			}
			_, _ = w.Write(payload)
		}))
		t.Cleanup(binaryServer.Close)
		t.Cleanup(unblock)
		manifestServer := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprintf(w, `{"version":"99.0.0","packageName":"@pi-in-go/pig","binaries":{%q:{"url":%q,"sha256":%q}}}`, codingagent.PlatformKey(), binaryServer.URL, hex.EncodeToString(digest[:]))
		}))
		t.Cleanup(manifestServer.Close)
		t.Setenv("PIG_UPDATE_URL", manifestServer.URL)
		var firstErr, secondErr, readErr error
		var retained []byte
		captureStdoutStderr(t, func() int {
			first := make(chan error, 1)
			go func() { first <- applyStandaloneUpdate(exe, false) }()
			select {
			case <-started:
			case firstErr = <-first:
				return 0
			}
			secondErr = applyStandaloneUpdate(exe, false)
			retained, readErr = os.ReadFile(exe)
			unblock()
			firstErr = <-first
			return 0
		})
		if runtime.GOOS == "windows" {
			// D39 refuses native in-place replacement on Windows before downloading.
			assert.Error(t, firstErr)
			assert.Zero(t, downloads.Load())
			return
		}
		assert.Error(t, secondErr, "the second update must not acquire an active native installation")
		assert.NoError(t, readErr)
		assert.Equal(t, "old", string(retained))
		assert.NoError(t, firstErr)
		assert.Equal(t, int32(1), downloads.Load())
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:695
	t.Run("keeps npm self-updates non-managed when the managed environment is inherited", func(t *testing.T) {
		f := newPackageCommandPathsFixture(t)
		inherited := filepath.Join(f.root, "inherited-managed-install")
		writeStartupFixtureFile(t, filepath.Join(inherited, "managed-install.json"), `{"kind":"pi-managed-install","schemaVersion":1,"layout":"releases-v1"}`)
		t.Setenv("PI_MANAGED_INSTALL_ROOT", inherited)
		t.Setenv("PIG_INSTALL_TIER", "")
		seedRunningStandaloneReceipt(t, "https://updates.invalid/manifest")
		provenance, err := codingagent.ResolveSelfUpdateTier()
		require.NoError(t, err)
		if runtime.GOOS == "windows" {
			assert.Equal(t, codingagent.TierUnsupported, provenance.Tier)
		} else {
			assert.Equal(t, codingagent.TierStandalone, provenance.Tier)
		}
		assert.Empty(t, provenance.PackageOwner)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:755
	t.Run("uses the current package name when the update check omits packageName", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"99.0.0","binaries":{}}`))
		}))
		t.Cleanup(server.Close)
		t.Setenv("PIG_UPDATE_URL", server.URL)
		stdout, stderr, code := capturePackageCommand(t, "update", "--self")
		// D39 requires an explicit signed package identity rather than inferring one.
		assert.Equal(t, 1, code)
		assert.Contains(t, stderr, `invalid package name ""`)
		assert.Empty(t, stdout)
	})

	// upstream: packages/coding-agent/test/package-command-paths.test.ts:896
	t.Run("fails self-update when renamed npm package installation fails", func(t *testing.T) {
		node := nodeExecutableForPackageCommand(t)
		f := newPackageCommandPathsFixture(t)
		script := filepath.Join(f.root, "fake-npm-fail.cjs")
		record := filepath.Join(f.root, "self-update-fail.json")
		t.Setenv("WAVE10_NPM_RECORD", record)
		writeStartupFixtureFile(t, script, `const fs=require("node:fs"),args=process.argv.slice(2),record=process.env.WAVE10_NPM_RECORD;
const calls=fs.existsSync(record)?JSON.parse(fs.readFileSync(record,"utf8")):[];
calls.push(args); fs.writeFileSync(record,JSON.stringify(calls));
if(args.includes("install")) process.exit(23);
`)
		server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"version":"0.73.0","packageName":"@new-scope/pi","binaries":{}}`))
		}))
		t.Cleanup(server.Close)
		t.Setenv("PIG_UPDATE_URL", server.URL)
		var updateErr error
		stdout, stderr, _ := captureStdoutStderr(t, func() int {
			updateErr = applyPackageManagerUpdate(&codingagent.SelfUpdateProvenance{Tier: codingagent.TierPackageManager, PackageOwner: "npm", PackageName: codingagent.PackageName, ExePath: filepath.Join(f.root, "pig")}, []string{node, script}, false)
			return 0
		})
		require.Error(t, updateErr)
		assert.Contains(t, updateErr.Error(), "exit status 23")
		assert.NotContains(t, stdout+stderr, "Updated")
		data, err := os.ReadFile(record)
		require.NoError(t, err)
		var calls [][]string
		require.NoError(t, json.Unmarshal(data, &calls))
		require.Len(t, calls, 2)
		assert.Subset(t, calls[0], []string{"uninstall", "-g", codingagent.PackageName})
		assert.Subset(t, calls[1], []string{"install", "-g", "@new-scope/pi@0.73.0"})
	})
}
