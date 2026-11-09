package host_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/MichaelKinsy/PiG/apps/p2/tui/host"
)

var token = strings.Repeat("a", 43)

func argsEnv(env map[string]string) func(string) string {
	return func(name string) string { return env[name] }
}

func TestJoinTakesTheOriginFromItsLinkOrMustMatchTheURLArgument(t *testing.T) {
	link := "https://share.test/join/" + token
	parsed, err := host.ParseArgs([]string{"--join", link}, argsEnv(nil))
	if err != nil || host.OriginOf(parsed.URL) != "https://share.test" || parsed.JoinLink != link || parsed.OwnerLink != "" {
		t.Fatalf("parsed = %+v, %v", parsed, err)
	}
	if parsed, err := host.ParseArgs([]string{"https://share.test", "--join", link}, argsEnv(nil)); err != nil || host.OriginOf(parsed.URL) != "https://share.test" {
		t.Fatalf("with url: %+v %v", parsed, err)
	}
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"https://other.test", "--join", link}, "share link for this origin"},
		{[]string{"--join", "http://share.test/join/x"}, "HTTPS"},
		{[]string{"--join", "https://share.test/join/" + token + "?x=1"}, "share link"},
		{[]string{"--join", link, "--owner-link", "https://share.test/owner#" + strings.Repeat("f", 64)}, "either"},
	} {
		if _, err := host.ParseArgs(c.args, argsEnv(nil)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Fatalf("%v: %v, want %q", c.args, err, c.want)
		}
	}
	// The environment's owner link never mixes into a join.
	if parsed, err := host.ParseArgs([]string{"--join", link}, argsEnv(map[string]string{"P2_TUI_OWNER_LINK": "x"})); err != nil || parsed.OwnerLink != "" {
		t.Fatalf("env owner link: %+v %v", parsed, err)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func reply(status int, body string, headers ...string) *http.Response {
	response := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	for i := 0; i+1 < len(headers); i += 2 {
		response.Header.Add(headers[i], headers[i+1])
	}
	return response
}

func TestRemoteGuestMixedPromptRedeemsAShareLinkRestartReusesTheCookieEndedSharesRePrompt(t *testing.T) {
	target := mustURL("https://share.test")
	config := host.Config{Connections: map[string]host.Connection{}}
	var mu sync.Mutex
	admitted := true
	type seen struct{ path, method, cookie, origin string }
	var requests []seen
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, seen{r.URL.Path, r.Method, r.Header.Get("Cookie"), r.Header.Get("Origin")})
		switch r.URL.Path {
		case "/owner/check":
			return reply(401, ""), nil
		case "/__p2_tui":
			if admitted {
				return reply(200, ""), nil
			}
			return reply(403, ""), nil
		case "/join":
			var body struct{ Token, Name string }
			data, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(data, &body)
			if !strings.EqualFold(body.Token, token) {
				return reply(400, `{"error":"This share has ended\u001b]52;c;x\u0007"}`), nil
			}
			if r.Header.Get("Cookie") != "" {
				return reply(200, `{"session":"shared-session"}`), nil
			}
			return reply(200, `{"session":"shared-session"}`, "Set-Cookie", "p2_guest_443=guest.secret; HttpOnly; SameSite=Strict; Path=/; Secure"), nil
		}
		t.Errorf("unexpected request %s", r.URL.Path)
		return reply(404, ""), nil
	})}
	var prompts []string
	answers := []string{host.OriginOf(target) + "/join/" + token, "  ", "Ada\x1b[31m Lovelace"}
	prompt := func(kind, _ string) (string, error) {
		prompts = append(prompts, kind)
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	}
	admission, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: client}, &config, prompt)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(prompts, ",") != "link,name,name" {
		t.Fatalf("an empty name is asked again: %v", prompts)
	}
	if want := (host.Admission{Credentials: host.Credentials{Cookie: "p2_guest_443=guest.secret"}, Role: "guest", Session: "shared-session"}); admission != want {
		t.Fatalf("admission = %+v", admission)
	}
	for _, r := range requests {
		if r.path == "/join" && (r.origin != "https://share.test" || r.cookie != "") {
			t.Fatalf("join request = %+v", r)
		}
	}
	if encoded, _ := json.Marshal(config); strings.Contains(string(encoded), token) {
		t.Fatal("the invitation is never stored")
	}

	directory := t.TempDir()
	if err := host.SaveConfig(config, directory); err != nil {
		t.Fatal(err)
	}
	reread, err := host.ReadConfig(directory)
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: client}, &reread, func(string, string) (string, error) {
		t.Fatal("Unexpected prompt")
		return "", nil
	})
	if err != nil || restarted.Role != "guest" || requests[len(requests)-1].cookie != "p2_guest_443=guest.secret" {
		t.Fatalf("restarted: %+v %v %+v", restarted, err, requests[len(requests)-1])
	}
	// Rejoining with the same link keeps the identity: the stored cookie goes along and is kept.
	reread, _ = host.ReadConfig(directory)
	again, err := host.Authenticate(context.Background(), host.AuthOptions{URL: target, JoinLink: host.OriginOf(target) + "/join/" + token, HTTP: client}, &reread, func(string, string) (string, error) { return "Ada", nil })
	if err != nil || again.Cookie != "p2_guest_443=guest.secret" || requests[len(requests)-1].cookie != "p2_guest_443=guest.secret" {
		t.Fatalf("again: %+v %v", again, err)
	}
	// The share was reset: the stored cookie is forgotten and the prompt returns; join errors are one plain line.
	mu.Lock()
	admitted = false
	mu.Unlock()
	reread, _ = host.ReadConfig(directory)
	_, err = host.Authenticate(context.Background(), host.AuthOptions{URL: target, HTTP: client}, &reread, func(kind, _ string) (string, error) {
		if kind == "link" {
			return host.OriginOf(target) + "/join/" + strings.Repeat("b", 43), nil
		}
		return "Ada", nil
	})
	if err == nil || err.Error() != "This share has ended]52;c;x" {
		t.Fatalf("error = %v", err)
	}
	if reread.Connections[host.OriginOf(target)].Guest != nil {
		t.Fatal("the ended guest cookie was kept")
	}
	host.ForgetGuest(&config, host.OriginOf(target))
	if config.Connections[host.OriginOf(target)].Guest != nil {
		t.Fatal("forgetGuest")
	}
}

func TestARefusedReconnectTheStoredCookieKindDecidesTheNoticeWhichCookieIsForgottenAndThePrompt(t *testing.T) {
	target := mustURL("https://share.test")
	origin := host.OriginOf(target)
	both := func() host.Config {
		return host.Config{Connections: map[string]host.Connection{origin: {Credentials: host.Credentials{Cookie: "owner=x"}, Guest: &host.Guest{Cookie: "p2_guest_1=a.b", Session: "s"}}}}
	}
	if host.EndedNotice("guest") != "This share has ended." || host.EndedNotice("owner") != "Signed out on this device." {
		t.Fatal("notices")
	}
	guestConfig := both()
	var questions []string
	answers := []string{"https://other.test/join/" + token, origin + "/join/" + token}
	next, err := host.ResumeAfterEnded("guest", target, &guestConfig, func(question string) (string, error) {
		questions = append(questions, question)
		answer := answers[0]
		answers = answers[1:]
		return answer, nil
	})
	if err != nil || next == nil || *next != (host.Resumed{JoinLink: origin + "/join/" + token}) {
		t.Fatalf("next = %+v %v", next, err)
	}
	if !strings.Contains(questions[0], "new share link") || !strings.Contains(questions[1], "share link for this origin") || !strings.Contains(questions[1], "Try again") {
		t.Fatalf("questions = %q", questions)
	}
	if got := guestConfig.Connections[origin]; got.Cookie != "owner=x" || got.Guest != nil {
		t.Fatalf("only the guest cookie is forgotten: %+v", got)
	}
	ownerConfig := both()
	owner := origin + "/owner#" + strings.Repeat("f", 64)
	next, err = host.ResumeAfterEnded("owner", target, &ownerConfig, func(string) (string, error) { return owner, nil })
	if err != nil || next == nil || *next != (host.Resumed{OwnerLink: owner}) {
		t.Fatalf("owner next = %+v %v", next, err)
	}
	if got := ownerConfig.Connections[origin]; got.Cookie != "" || got.Guest == nil || *got.Guest != (host.Guest{Cookie: "p2_guest_1=a.b", Session: "s"}) {
		t.Fatalf("owner config = %+v", got)
	}
	fresh := both()
	if next, err := host.ResumeAfterEnded("guest", target, &fresh, func(string) (string, error) { return "  ", nil }); err != nil || next != nil {
		t.Fatalf("Enter quits: %+v %v", next, err)
	}
}

func TestLoopbackStaysTheLocalOwnerAStoredGuestCookieNeverDowngradesIt(t *testing.T) {
	l := startLauncher(t, launcherOptions{})
	origin := host.OriginOf(l.url)
	config := host.Config{Connections: map[string]host.Connection{origin: {Guest: &host.Guest{Cookie: "p2_guest_1=x.y", Session: "s"}}}}
	admission, err := host.Authenticate(context.Background(), host.AuthOptions{URL: l.url, DataDir: l.fixture.DataDir}, &config, func(string, string) (string, error) {
		t.Fatal("Unexpected prompt")
		return "", nil
	})
	if err != nil || admission.Role != "owner" || !admission.Local || admission.Cookie == "" {
		t.Fatalf("admission = %+v %v", admission, err)
	}
}

func TestConfigIsPrivateAndOnlyCookiesSurvive(t *testing.T) {
	directory := t.TempDir()
	config := host.Config{Connections: map[string]host.Connection{"https://x.test": {Credentials: host.Credentials{Cookie: "c=1"}}}}
	if err := host.SaveConfig(config, directory); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory + "/config.json")
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("config.json mode %v %v", info, err)
	}
}
