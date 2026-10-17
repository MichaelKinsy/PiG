//go:build !pig_strip_self_update

package cli

import (
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
)

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
