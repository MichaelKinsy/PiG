package codingagent

import (
	"slices"
	"testing"
)

// Pi cea25743 session-share.test.ts "forces gist sharing without checking Radius authentication" and "reports missing Radius authentication when Radius is forced": Pi's /share reads sessionShareDestination and sends "gist" straight to a GitHub gist and "radius" only to Radius.
// pig divergence (D64): PiG's /share has one destination, the PiG gateway, so every value, including Pi's forced "radius" without Radius auth, still runs the PiG upload and reports no Radius error.
func TestShareHandlerUploadsToPiGForEveryShareDestination(t *testing.T) {
	for _, destination := range []SessionShareDestination{SessionShareDestinationAuto, SessionShareDestinationGist, SessionShareDestinationRadius} {
		t.Run(string(destination), func(t *testing.T) {
			sm := NewSettingsManager(t.TempDir(), t.TempDir())
			if err := sm.SetSessionShareDestination(destination); err != nil {
				t.Fatal(err)
			}
			if got := sm.GetSessionShareDestination(); got != destination {
				t.Fatalf("destination = %q, want %q", got, destination)
			}
			session := shareTestSession(t, "destination-"+string(destination))
			sc, out := newFakeSlashCtx()
			sc.SettingsManager = sm
			sc.CurrentSession = func() *Session { return session }
			uploads := 0
			sc.ShareSession = func(got *Session, _ ShareState, _ func(string)) (string, error) {
				if got != session {
					t.Fatalf("shared session = %p, want %p", got, session)
				}
				uploads++
				return "Share URL: https://pi-in-go.dev/session/p_destination", nil
			}
			if err := shareHandler(sc); err != nil {
				t.Fatal(err)
			}
			if uploads != 1 || out.String() != "Share URL: https://pi-in-go.dev/session/p_destination\n" {
				t.Fatalf("uploads = %d, output = %q", uploads, out.String())
			}
		})
	}
}

// Pi cea25743 settings-selector.test.ts "cycles through share destinations": Pi's /settings has a "share-destination" item cycling auto, gist, radius.
// pig divergence (D64): PiG's /share ignores the setting, so /settings offers no item that would change nothing.
func TestSettingsSelectorOmitsShareDestination(t *testing.T) {
	initSettingsSelectorTheme(t)
	list := NewSettingsSelectorComponent(SettingsConfig{DefaultModel: "not set"}, SettingsCallbacks{}).GetSettingsList()
	ids := make([]string, 0, len(list.Items()))
	for _, item := range list.Items() {
		ids = append(ids, item.ID)
	}
	if len(ids) == 0 {
		t.Fatal("settings selector has no items")
	}
	if slices.Contains(ids, "share-destination") {
		t.Fatalf("settings selector items %v include share-destination", ids)
	}
}
