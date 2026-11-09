// Adversarial cases of the loopback owner change (Pi 7d073328) beyond the ported ones: a cross-site or same-site page driving
// the local sign-in and the invitation route, forged and replayed credentials, and a forwarded or tunnel request that
// claims to be local. Review additions (RevP2Next).

package e2e_test

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/hosts"
	"github.com/MichaelKinsy/PiG/apps/p2/p2test"
	p2runtime "github.com/MichaelKinsy/PiG/apps/p2/runtime"
)

// rawGet sends a GET to the server with header lines exactly as given (a name may repeat).
func rawGet(t *testing.T, port int, path string, lines [][2]string) int {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range lines {
		request.Header.Add(line[0], line[1])
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	return response.StatusCode
}

// GET /owner-token answers a local owner only with no Fetch Metadata or `same-origin` (Pi: `site !== undefined && site !==
// "same-origin"`): not `none` (a typed address), not `same-site`, `cross-site` nor an empty value.
func TestOwnerTokenFetchMetadataRuleIsExact(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port, cookie := fixture.Server.Port, fixture.Owner().Cookie
	for site, want := range map[string]int{"same-origin": 200, "none": 403, "same-site": 403, "cross-site": 403, "": 403, "SAME-ORIGIN": 403} {
		if got := rawGet(t, port, "/owner-token", [][2]string{{"Cookie", cookie}, {"Sec-Fetch-Site", site}}); got != want {
			t.Errorf("Sec-Fetch-Site %q: %d, want %d", site, got, want)
		}
	}
	if got := rawGet(t, port, "/owner-token", [][2]string{{"Cookie", cookie}}); got != 200 {
		t.Errorf("no Fetch Metadata: %d", got)
	}
	// A navigation (the one foreign request that authenticates) from another site never mints.
	for _, site := range []string{"cross-site", "same-site", "none"} {
		lines := [][2]string{{"Cookie", cookie}, {"Sec-Fetch-Site", site}, {"Sec-Fetch-Mode", "navigate"}, {"Sec-Fetch-Dest", "document"}}
		if got := rawGet(t, port, "/owner-token", lines); got != 403 && got != 401 {
			t.Errorf("navigation from %s: %d", site, got)
		}
	}
}

// Node joins repeated header lines with ", ", so `same-origin` + `cross-site` is not `same-origin` there. Go's route rule reads the
// first value only. A browser never sends two Sec-Fetch-Site lines (a forbidden header), so this is a fidelity nit, not an
// exposure; the case keeps the port equal to Pi.
func TestOwnerTokenRefusesARepeatedFetchMetadataLine(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	got := rawGet(t, fixture.Server.Port, "/owner-token", [][2]string{{"Cookie", fixture.Owner().Cookie}, {"Sec-Fetch-Site", "same-origin"}, {"Sec-Fetch-Site", "cross-site"}})
	if got != 403 && got != 401 {
		t.Fatalf("repeated Sec-Fetch-Site: %d (Pi: refused)", got)
	}
}

// A page of another site or another local port cannot obtain a challenge or a session, with the right Origin or without it.
func TestLocalSignInIsNotDrivenByAnotherSitesPage(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port := fixture.Server.Port
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	key, err := os.ReadFile(filepath.Join(fixture.DataDir, "owner-key"))
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, body any, headers map[string]string) p2test.HTTPResult {
		encoded, _ := json.Marshal(body)
		return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(headers, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader(string(encoded))})
	}
	for name, headers := range map[string]map[string]string{
		"cross-site fetch with a forged Origin": {"Origin": origin, "Sec-Fetch-Site": "cross-site", "Sec-Fetch-Mode": "cors", "Sec-Fetch-Dest": "empty"},
		"another local port":                    {"Origin": "http://127.0.0.1:" + strconv.Itoa(port+1), "Sec-Fetch-Site": "same-site"},
		"localhost alias":                       {"Origin": "http://localhost:" + strconv.Itoa(port), "Sec-Fetch-Site": "same-site"},
		"null origin":                           {"Origin": "null", "Sec-Fetch-Site": "cross-site"},
	} {
		if got := post(p2runtime.LocalChallengePath, map[string]any{}, headers); got.Status != 403 {
			t.Errorf("%s: a challenge was given: %d %q", name, got.Status, got.Body)
		}
		// Even holding a valid challenge and the key, a foreign page's claim gets no cookie.
		challenge := httpGet(t, port, p2runtime.LocalChallengePath, p2test.HTTPOptions{Method: http.MethodPost, Headers: map[string]string{"Origin": origin, "Content-Type": "application/json"}, Body: strings.NewReader("{}")})
		var value struct {
			Challenge string `json:"challenge"`
		}
		if err := json.Unmarshal([]byte(challenge.Body), &value); err != nil || value.Challenge == "" {
			t.Fatalf("challenge: %d %q", challenge.Status, challenge.Body)
		}
		claimed := post(p2runtime.LocalClaimPath, map[string]any{"challenge": value.Challenge, "proof": p2runtime.LocalOwnerProof(strings.TrimSpace(string(key)), port, value.Challenge)}, headers)
		if claimed.Status == 200 || claimed.Headers.Get("Set-Cookie") != "" {
			t.Errorf("%s: a session was given: %d %q", name, claimed.Status, claimed.Headers.Get("Set-Cookie"))
		}
	}
}

// A challenge is single use even after a correct answer; an owner link is single use; an answer for one challenge is not one for
// another; padded and oversized bodies are refused.
func TestReplayedChallengesAndInvitationsAreRefused(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{})
	port := fixture.Server.Port
	origin := "http://127.0.0.1:" + strconv.Itoa(port)
	keyText, _ := os.ReadFile(filepath.Join(fixture.DataDir, "owner-key"))
	key := strings.TrimSpace(string(keyText))
	same := map[string]string{"Origin": origin, "Content-Type": "application/json"}
	post := func(path, body string) p2test.HTTPResult {
		return httpGet(t, port, path, p2test.HTTPOptions{Method: http.MethodPost, Headers: same, Body: strings.NewReader(body)})
	}
	challenge := func() string {
		var value struct {
			Challenge string `json:"challenge"`
		}
		_ = json.Unmarshal([]byte(post(p2runtime.LocalChallengePath, "{}").Body), &value)
		return value.Challenge
	}
	answer := func(c, other string) string {
		encoded, _ := json.Marshal(map[string]any{"challenge": c, "proof": p2runtime.LocalOwnerProof(key, port, other)})
		return string(encoded)
	}
	first, second := challenge(), challenge()
	if got := post(p2runtime.LocalClaimPath, answer(first, second)).Status; got != 403 {
		t.Errorf("another challenge's answer: %d", got)
	}
	if got := post(p2runtime.LocalClaimPath, answer(second, second)).Status; got != 200 {
		t.Fatalf("the right answer: %d", got)
	}
	if got := post(p2runtime.LocalClaimPath, answer(second, second)).Status; got != 403 {
		t.Errorf("a replayed, once-correct answer: %d", got)
	}
	if got := post(p2runtime.LocalClaimPath, answer(" "+challenge()+" ", "")).Status; got != 403 {
		t.Errorf("a padded challenge: %d", got)
	}
	big, _ := json.Marshal(map[string]any{"challenge": challenge(), "proof": strings.Repeat("a", 4096)})
	if got := post(p2runtime.LocalClaimPath, string(big)).Status; got != 413 {
		t.Errorf("an oversized body: %d", got)
	}
	// Owner links: once.
	minted := httpGet(t, port, "/owner-token", p2test.HTTPOptions{Headers: map[string]string{"Cookie": fixture.Owner().Cookie}})
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(minted.Body), &token); err != nil || token.Token == "" {
		t.Fatalf("mint: %d %q", minted.Status, minted.Body)
	}
	if got := claimWith(t, port, tokenBody(token.Token), same).Status; got != 200 {
		t.Fatalf("redeem: %d", got)
	}
	replayed := claimWith(t, port, tokenBody(token.Token), same)
	if replayed.Status != 403 || replayed.Headers.Get("Set-Cookie") != "" {
		t.Errorf("a replayed owner link: %d %q", replayed.Status, replayed.Headers.Get("Set-Cookie"))
	}
	// A cross-site page cannot redeem even a fresh one.
	minted = httpGet(t, port, "/owner-token", p2test.HTTPOptions{Headers: map[string]string{"Cookie": fixture.Owner().Cookie}})
	_ = json.Unmarshal([]byte(minted.Body), &token)
	if got := claimWith(t, port, tokenBody(token.Token), merged(same, map[string]string{"Sec-Fetch-Site": "cross-site"})); got.Status != 403 || got.Headers.Get("Set-Cookie") != "" {
		t.Errorf("a cross-site redemption: %d %q", got.Status, got.Headers.Get("Set-Cookie"))
	}
	if got := claimWith(t, port, tokenBody(token.Token), same).Status; got != 200 {
		t.Errorf("the refused cross-site attempt must not have used the invitation: %d", got)
	}
}

// Forged session cookies: the `local.` marker is under the signature, so neither dropping it, adding it to a device cookie,
// nor changing the expiry or the id yields a session.
func TestForgedSessionCookiesAreNobody(t *testing.T) {
	p2test.NoLeakedGoroutines(t)
	fixture := p2test.StartFixture(t, hosts.ServerOptions{AllowedHosts: []string{"owner.test"}})
	port := fixture.Server.Port
	name := "p2_owner_" + strconv.Itoa(port)
	local := strings.TrimPrefix(fixture.Owner().Cookie, name+"=") // local.<id>.<expiry>.<sig>
	parts := strings.Split(local, ".")
	if len(parts) != 4 || parts[0] != "local" {
		t.Fatalf("cookie %q", local)
	}
	other := strings.Repeat("0", 32)
	for forged, value := range map[string]string{
		"marker dropped":      strings.Join(parts[1:], "."),
		"expiry extended":     strings.Join([]string{"local", parts[1], "99999999999999", parts[3]}, "."),
		"another id":          strings.Join([]string{"local", other, parts[2], parts[3]}, "."),
		"uppercase marker":    "LOCAL." + strings.Join(parts[1:], "."),
		"marker twice":        "local." + local,
		"signature zeroed":    strings.Join([]string{"local", parts[1], parts[2], strings.Repeat("0", 64)}, "."),
		"empty signature":     strings.Join([]string{"local", parts[1], parts[2], ""}, "."),
		"trailing junk":       local + "x",
		"the key as a cookie": strings.TrimSpace(readKey(t, fixture.DataDir)),
	} {
		if got := statusOf(t, port, "/owner/check", map[string]string{"Cookie": name + "=" + value}); got != 401 {
			t.Errorf("%s: %d", forged, got)
		}
	}
	// A real local session is nobody through a tunnel host or a forwarded request, whatever else is claimed.
	valid := fixture.Owner().Cookie
	for label, headers := range map[string]map[string]string{
		"tunnel host":               {"Host": "owner.test", "Origin": "https://owner.test", "Cookie": valid},
		"tunnel host claiming loop": {"Host": "owner.test", "Origin": "https://owner.test", "Cookie": valid, "X-Forwarded-For": "127.0.0.1", "Forwarded": "for=127.0.0.1"},
		"loopback host forwarded":   {"Cookie": valid, "X-Forwarded-For": "203.0.113.9"},
		"loopback host proxy mark":  {"Cookie": valid, "X-P2-Proxy-Secret": "x"},
	} {
		if got := statusOf(t, port, "/owner/check", headers); got == 200 {
			t.Errorf("%s: the local session was accepted", label)
		}
		if got := statusOf(t, port, "/owner-token", headers); got == 200 {
			t.Errorf("%s: an invitation was minted", label)
		}
		challenge := httpGet(t, port, p2runtime.LocalChallengePath, p2test.HTTPOptions{Method: http.MethodPost, Headers: merged(headers, map[string]string{"Content-Type": "application/json"}), Body: strings.NewReader("{}")})
		if challenge.Status == 200 {
			t.Errorf("%s: a challenge was given", label)
		}
	}
}

func readKey(t *testing.T, dataDir string) string {
	t.Helper()
	text, err := os.ReadFile(filepath.Join(dataDir, "owner-key"))
	if err != nil {
		t.Fatal(err)
	}
	return string(text)
}

// Chromium: a page on another local port (same site, other origin) can neither read nor drive p2 with the owner's cookie; its
// link opens the app for the owner's browser, a browser without a p2 cookie gets the sign-in page (ui/foreign.mjs; Pi's case
// of test/p2/foreign-requests.test.ts that the port did not carry). Skipped without P2_UI_NODE_PATH.
func TestChromiumPageOnAnotherPortCannotReadOrDriveP2(t *testing.T) {
	if os.Getenv("P2_UI_NODE_PATH") == "" {
		t.Skip("P2_UI_NODE_PATH (node_modules with playwright-core) is not set")
	}
	enabled := true
	fixture := p2test.StartFixture(t, hosts.ServerOptions{Web: &enabled})
	command := exec.Command("node", "ui/foreign.mjs", "http://127.0.0.1:"+strconv.Itoa(fixture.Server.Port))
	command.Env = append(os.Environ(), "P2_UI_COOKIE="+fixture.Owner().Cookie)
	output, err := command.CombinedOutput()
	t.Logf("%s", output)
	if err != nil {
		t.Fatal(err)
	}
}
