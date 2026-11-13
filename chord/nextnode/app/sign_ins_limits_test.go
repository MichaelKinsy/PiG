package app

import (
	"fmt"
	"testing"

	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/chord/next/host"
)

// Ports the start option of packages/chord/test/node/app/sign-ins.test.ts, "live at most `limits.ephemeralSignIns`: past it
// the oldest ends" (Pi ff7414d51: `limits.ephemeralSessions` until then): Pi's test gives the limit to the app (`testApp({
// limits: { ephemeralSignIns: 3 } })`); the kit-level twin, with Pi's 2,000 issues, is
// TestEphemeralSignInsLiveAtMostTheLimitPastItTheOldestEndsAnIssueCostsTheSameWhateverCameBefore. Here the whole app
// is started with AppLimits.EphemeralSignIns = 3 and the fourth ephemeral sign-in ends the first.
func TestStartServerGivesLimitsEphemeralSignInsToTheKitsSignIns(t *testing.T) {
	s := startTestApp(t, nil, func(options *AppOptions) { options.Limits.EphemeralSignIns = 3 })
	signIns, err := s.app.Host.Get(background, SignIns, delta.NewJsonObject(0), host.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer signIns.Release()
	var tokens []string
	for index := range 4 {
		principal := delta.JsonObjectOf("kind", "user", "id", fmt.Sprintf("u%d", index), "name", "u")
		issued, err := signIns.Call("issue", delta.JsonObjectOf("principal", principal, "label", "u", "kind", "ephemeral"), background)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, issued.(*delta.JsonObject).Value("token").(string))
	}
	var alive []bool
	for _, token := range tokens {
		principal, err := signIns.Call("verify", token, background)
		if err != nil {
			t.Fatal(err)
		}
		alive = append(alive, principal != nil)
	}
	if fmt.Sprint(alive) != "[false true true true]" {
		t.Fatalf("alive = %v, want the first ended and the last three alive", alive)
	}
}
