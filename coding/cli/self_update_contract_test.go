//go:build !pig_strip_self_update

package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// upstream: packages/coding-agent/src/utils/version-check.ts:51-72; utils/management-http.ts:3,47-77
func TestExplicitUpdateManifestRetryBoundary(t *testing.T) {
	for _, status := range []int{408, 425, 429, 500, 502, 503, 504, 400, 401, 403, 404} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			newPackageCommandPathsFixture(t)
			var requests atomic.Int32
			server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(status)
			}))
			t.Cleanup(server.Close)
			t.Setenv("PIG_UPDATE_URL", server.URL)
			_, stderr, code := capturePackageCommand(t, "update", "--self")
			assert.Equal(t, 1, code)
			assert.Contains(t, stderr, http.StatusText(status))
			want := int32(1)
			if status == 408 || status == 425 || status == 429 || status >= 500 {
				want = 3 // Pi maxRetries=2 means one initial request plus two retries.
			}
			assert.Equal(t, want, requests.Load())
		})
	}
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("parse failure retry=%t", retry), func(t *testing.T) {
			newPackageCommandPathsFixture(t)
			var requests atomic.Int32
			server := signedManifestServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				_, _ = w.Write([]byte(`{`))
			}))
			t.Cleanup(server.Close)
			_, err := codingagent.FetchUpdateManifest(t.Context(), server.Client(), server.URL, codingagent.FetchUpdateManifestOptions{Retry: retry})
			require.ErrorContains(t, err, "parse update manifest")
			assert.Equal(t, int32(1), requests.Load())
		})
	}
	t.Run("startup does not retry", func(t *testing.T) {
		newPackageCommandPathsFixture(t)
		var attempts int
		client := &http.Client{Transport: packageUpdateTransport(func(*http.Request) (*http.Response, error) {
			attempts++
			return nil, errors.New("fetch failed")
		})}
		_, err := codingagent.FetchUpdateManifest(t.Context(), client, "https://updates.invalid/manifest")
		require.ErrorContains(t, err, "fetch failed")
		assert.Equal(t, 1, attempts)
	})
}

// upstream: packages/coding-agent/src/utils/version-check.ts:6,68; utils/management-http.ts:43-77
func TestExplicitUpdateManifestCancellationBudget(t *testing.T) {
	newPackageCommandPathsFixture(t)
	for _, canceled := range []bool{true, false} {
		t.Run(fmt.Sprintf("canceled=%t", canceled), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if canceled {
					cancel()
				}
				attempts := 0
				client := &http.Client{Transport: packageUpdateTransport(func(request *http.Request) (*http.Response, error) {
					attempts++
					<-request.Context().Done()
					return nil, request.Context().Err()
				})}
				start := time.Now()
				_, err := codingagent.FetchUpdateManifest(ctx, client, "https://updates.invalid/manifest", codingagent.FetchUpdateManifestOptions{Retry: true})
				if canceled {
					assert.ErrorIs(t, err, context.Canceled)
					assert.Zero(t, attempts)
					assert.Zero(t, time.Since(start))
				} else {
					assert.ErrorIs(t, err, context.DeadlineExceeded)
					assert.Equal(t, 1, attempts)
					assert.Equal(t, 10*time.Second, time.Since(start))
				}
			})
		})
	}
}

// upstream: packages/coding-agent/src/package-manager-cli.ts:171-222; D39 protects native replacement through receipt commit and rollback instead of npm release activation.
func TestStandaloneUpdateLocksThroughReceiptRollback(t *testing.T) {
	if target := os.Getenv("PIG_TEST_UPDATE_LOCK_TARGET"); target != "" {
		bin := codingagent.UpdateBinary{URL: os.Getenv("PIG_TEST_UPDATE_LOCK_URL"), SHA256: os.Getenv("PIG_TEST_UPDATE_LOCK_SHA256")}
		err := codingagent.SelfReplaceAt(t.Context(), http.DefaultClient, bin, target)
		require.EqualError(t, err, "another standalone pig update is already running")
		return
	}
	f := newPackageCommandPathsFixture(t)
	t.Setenv("PIG_UPDATE_ALLOW_LOOPBACK_HTTP", "1")
	target := filepath.Join(f.root, "pig")
	writeStartupFixtureFile(t, target, "old")
	payload := []byte("new release")
	digest := sha256.Sum256(payload)
	var downloads atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downloads.Add(1)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	bin := codingagent.UpdateBinary{URL: server.URL, SHA256: hex.EncodeToString(digest[:])}
	if runtime.GOOS == "windows" {
		require.ErrorContains(t, codingagent.SelfReplaceAt(t.Context(), server.Client(), bin, target), "not supported on Windows")
		assert.Zero(t, downloads.Load())
		return
	}
	alias := filepath.Join(f.root, "pig-alias")
	testenv.Symlink(t, target, alias)
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	result := make(chan error, 1)
	joined := false
	t.Cleanup(func() {
		unblock()
		if !joined {
			<-result
		}
	})
	commitErr := errors.New("receipt write failed")
	go func() {
		result <- codingagent.SelfReplaceAtWithCommit(t.Context(), server.Client(), bin, target, func() error {
			close(entered)
			<-release
			return commitErr
		})
	}()
	select {
	case <-entered:
	case err := <-result:
		joined = true
		t.Fatalf("replacement failed before commit: %v", err)
	}
	assert.FileExists(t, target+".update.lock")
	exe, err := os.Executable()
	require.NoError(t, err)
	child := exec.CommandContext(t.Context(), exe, "-test.run=^TestStandaloneUpdateLocksThroughReceiptRollback$")
	child.Env = append(os.Environ(), "PIG_TEST_UPDATE_LOCK_TARGET="+alias, "PIG_TEST_UPDATE_LOCK_URL="+bin.URL, "PIG_TEST_UPDATE_LOCK_SHA256="+bin.SHA256)
	output, err := child.CombinedOutput()
	require.NoError(t, err, "%s", output)
	assert.FileExists(t, target+".update.lock", "a refused updater must not remove the live holder's lock")
	assert.Equal(t, int32(1), downloads.Load())
	unblock()
	err = <-result
	joined = true
	require.ErrorIs(t, err, commitErr)
	retained, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "old", string(retained))
	// Pi's proper-lockfile release removes its lock (package-manager-cli.ts:219); the rollback path leaves no sidecar either.
	assert.NoFileExists(t, target+".update.lock")
	// The lock is released after rollback, so the next explicit operation succeeds.
	require.NoError(t, codingagent.SelfReplaceAt(t.Context(), server.Client(), bin, alias))
	assert.NoFileExists(t, target+".update.lock")
	retained, err = os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, payload, retained)
	assert.Equal(t, int32(2), downloads.Load())
}
