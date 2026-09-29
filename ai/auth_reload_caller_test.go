package ai

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/MichaelKinsy/PiG/internal/pilock"
)

func TestResolveProviderAuthSharesFileReloadIsolation(t *testing.T) {
	for _, oauth := range []bool{false, true} {
		name := "API key"
		if oauth {
			name = "OAuth"
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				isolateAuthReloadState(t)
				first, path := authReloadFile(t, `{}`)
				second, err := NewAuthStorage(path)
				if err != nil {
					t.Fatal(err)
				}
				auth := ProviderAuth{APIKey: EnvAPIKeyAuth("key", "PROBE_KEY")}
				credential := Credential{Type: CredentialAPIKey, Key: "fresh-key"}
				if oauth {
					auth = ProviderAuth{OAuth: &OAuthAuth{ToAuth: func(c Credential) (ModelAuth, error) {
						return ModelAuth{APIKey: c.Access, BaseURL: "https://custom.invalid/v1"}, nil
					}}}
					credential = Credential{Type: CredentialOAuth, Access: "fresh-key", Refresh: "refresh", Expires: time.Now().Add(time.Hour).UnixMilli()}
				}
				if err := second.Set("provider", credential); err != nil {
					t.Fatal(err)
				}
				acquire := acquireAuthFileLock
				grant := make(chan struct{})
				acquireAuthFileLock = func(ctx context.Context, path string) (*pilock.Lock, error) {
					<-grant
					return acquire(ctx, path)
				}
				t.Cleanup(func() { acquireAuthFileLock = acquire })
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				firstDone, secondDone := make(chan error, 1), make(chan error, 1)
				go func() {
					_, err := ResolveProviderAuth(ctx, "provider", auth, first, testAuthContext(nil), AuthResolutionOverrides{})
					firstDone <- err
				}()
				go func() {
					result, err := ResolveProviderAuth(t.Context(), "provider", auth, second, testAuthContext(nil), AuthResolutionOverrides{})
					if err == nil && (result == nil || result.Auth.APIKey != "fresh-key") {
						err = errors.New("provider received stale auth")
					}
					secondDone <- err
				}()
				synctest.Wait()
				cancel()
				if err := <-firstDone; !errors.Is(err, context.Canceled) {
					t.Errorf("canceled auth resolution=%v", err)
				}
				close(grant)
				if err := <-secondDone; err != nil {
					t.Fatal(err)
				}
			})
		})
	}
}

func TestAuthStorageSharedReloadSnapshotsAreIsolated(t *testing.T) {
	isolateAuthReloadState(t)
	first, path := authReloadFile(t, `{"custom":{"type":"api_key","key":"key","env":{"REGION":"eu"},"gatewayConfig":{"baseUrl":"https://custom.invalid"},"account":{"id":"one"}}}`)
	second, err := NewAuthStorage(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"Read", "Load", "GetRaw"} {
		var credential Credential
		switch mode {
		case "Read":
			got, err := first.Read(t.Context(), "custom")
			if err != nil || got == nil {
				t.Fatalf("read=%v,%v", got, err)
			}
			credential = *got
		case "Load":
			got, err := first.Load()
			if err != nil {
				t.Fatal(err)
			}
			credential = got["custom"]
		case "GetRaw":
			var err error
			credential, _, err = first.GetRaw("custom")
			if err != nil {
				t.Fatal(err)
			}
		}
		credential.Env["REGION"] = "poisoned"
		credential.GatewayConfig[0] = 'X'
		credential.Extra["account"][0] = 'X'
		got, _, err := second.GetRaw("custom")
		if err != nil || got.Env["REGION"] != "eu" || string(got.GatewayConfig) != `{"baseUrl":"https://custom.invalid"}` || string(got.Extra["account"]) != `{"id":"one"}` {
			t.Fatalf("%s poisoned shared snapshot: %+v, %v", mode, got, err)
		}
	}
}
