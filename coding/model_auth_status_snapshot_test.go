package coding

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/ai"
)

// Pi model-runtime.ts:562-577 reads stored credentials from the published snapshot, and provider-composer.ts:597-610 evaluates a configured "$VAR" key against the process environment only. A credential written after the snapshot cannot change the status, and the getter performs no credential I/O.
func TestProviderAuthStatusConfiguredKeyUsesSnapshotAndProcessEnv(t *testing.T) {
	const variable = "PIG_CONFIGURED_AUTH_SNAPSHOT_KEY"
	t.Setenv(variable, "")
	if err := os.Unsetenv(variable); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	services := registryTestServices(t, dir, map[string]any{"custom-provider": registryProviderWithKey("${" + variable + "}")})
	runtime := services.ModelRuntime()
	if got := runtime.GetProviderAuthStatus("custom-provider"); got != (ai.AuthStatus{}) {
		t.Fatalf("initial status = %+v, want unconfigured", got)
	}

	t.Run("unrefreshed credential env", func(t *testing.T) {
		// A second process writes the credential; this runtime has not refreshed its snapshot.
		data := `{"custom-provider":{"type":"api_key","key":"stored","env":{"` + variable + `":"from-credential"}}}`
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := runtime.GetProviderAuthStatus("custom-provider"); got != (ai.AuthStatus{}) {
			t.Fatalf("status after unrefreshed credential env = %+v, want unconfigured", got)
		}
	})

	t.Run("process env", func(t *testing.T) {
		t.Setenv(variable, "from-process")
		want := ai.AuthStatus{Configured: true, Source: ai.AuthSourceEnvironment, Label: variable}
		if got := runtime.GetProviderAuthStatus("custom-provider"); !reflect.DeepEqual(got, want) {
			t.Fatalf("status = %+v, want %+v", got, want)
		}
	})

	t.Run("does not wait for credential work", func(t *testing.T) {
		entered, release := make(chan struct{}), make(chan struct{})
		modified := make(chan error, 1)
		go func() {
			_, err := services.Auth().Modify(context.Background(), "other-provider", func(current *ai.Credential) (*ai.Credential, error) {
				close(entered)
				<-release
				return current, nil
			})
			modified <- err
		}()
		<-entered
		// A changed auth.json revision makes any credential read reload under the storage lock the callback holds.
		data := `{"custom-provider":{"type":"api_key","key":"stored-again","env":{"OTHER":"value"}}}`
		if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		status := make(chan ai.AuthStatus, 1)
		go func() { status <- runtime.GetProviderAuthStatus("custom-provider") }()
		// The deadline only detects a regression; a correct getter returns while the credential callback is still held.
		select {
		case <-status:
		case <-time.After(10 * time.Second):
			close(release)
			<-status
			<-modified
			t.Fatal("GetProviderAuthStatus waited for an unrelated credential operation")
		}
		close(release)
		if err := <-modified; err != nil {
			t.Fatal(err)
		}
	})
}
