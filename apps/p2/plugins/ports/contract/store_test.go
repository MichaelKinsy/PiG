package contract

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// Ports the first two cases of test/p2/ports.test.ts (Pi main c23b9ec1).

func text(value string) *string { return &value }

func wantError(t *testing.T, err error, pattern string) {
	t.Helper()
	if err == nil || !regexp.MustCompile(pattern).MatchString(err.Error()) {
		t.Fatalf("error = %v, want one matching %q", err, pattern)
	}
}

func ports(t *testing.T, forwards *Forwards) []int {
	t.Helper()
	list := []int{}
	for _, forward := range forwards.List() {
		list = append(list, forward.Port)
	}
	return list
}

func reopened(t *testing.T, directory string) *Forwards {
	t.Helper()
	forwards, err := OpenForwards(directory)
	if err != nil {
		t.Fatal(err)
	}
	return forwards
}

func equalPorts(got, want []int) bool {
	if len(got) != len(want) {
		return false
	}
	for at := range got {
		if got[at] != want[at] {
			return false
		}
	}
	return true
}

func TestForwardsPersistValidatePortsPreserveOwnershipAndInvalidateRemovedIDs(t *testing.T) {
	directory := t.TempDir()
	forwards := reopened(t, directory)
	var web, api Forward
	var errs [2]error
	var group sync.WaitGroup
	group.Add(2)
	go func() { defer group.Done(); web, errs[0] = forwards.Add(5173, text(" web "), []int{8788}, false, "") }()
	go func() { defer group.Done(); api, errs[1] = forwards.Add(3000, nil, []int{8788}, false, "") }()
	group.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatal(errs)
	}
	if !regexp.MustCompile(`^[a-z0-9]{10}$`).MatchString(web.ID) || web.Name != "web" || web.ID == api.ID {
		t.Fatalf("web = %+v, api = %+v", web, api)
	}
	_, err := forwards.Add(8788, text("pi"), []int{8788}, false, "")
	wantError(t, err, `Pi's own port`)
	_, err = forwards.Add(70000, nil, nil, false, "")
	wantError(t, err, `1 to 65535`)
	_, err = forwards.Add(12, text(strings.Repeat("x", 41)), nil, false, "")
	wantError(t, err, `at most`)
	if got := ports(t, reopened(t, directory)); !equalPorts(got, []int{3000, 5173}) {
		t.Fatalf("persisted ports = %v", got)
	}
	if err := forwards.SetPublic(api.ID, true); err != nil {
		t.Fatal(err)
	}
	if forward, _ := reopened(t, directory).Get(api.ID); !forward.Public {
		t.Fatal("public is not persisted")
	}
	if err := forwards.SetPublic(api.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := forwards.Remove(web.ID); err != nil {
		t.Fatal(err)
	}
	wantError(t, forwards.Remove(web.ID), `Unknown forward`)
	again, err := forwards.Add(5173, text("web"), nil, false, "")
	if err != nil || again.ID == web.ID {
		t.Fatalf("a re-added port keeps the removed ID: %+v, %v", again, err)
	}
	own, err := forwards.OpenForSession("one", 4000, OpenOptions{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	yes := true
	if opened, err := forwards.OpenForSession("one", 4000, OpenOptions{Public: &yes}, nil); err != nil || opened.ID != own.ID {
		t.Fatalf("opening a forwarded port again = %+v, %v", opened, err)
	}
	// Another session may change it (Pi Mobile 67cdb4ca4): it keeps its address and its opener.
	no := false
	changed, err := forwards.OpenForSession("two", 4000, OpenOptions{Public: &no, Name: text("renamed")}, nil)
	if err != nil || changed != (Forward{ID: own.ID, Port: 4000, Name: "renamed", Session: "one"}) {
		t.Fatalf("another session's open = %+v, %v", changed, err)
	}
	if err := forwards.CloseSession("one"); err != nil {
		t.Fatal(err)
	}
	if _, found := forwards.Get(own.ID); found {
		t.Fatal("the session's forward survived its closing")
	}
	// ...and close any forward, the owner's too.
	owners, err := forwards.Add(4001, nil, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if closed, err := forwards.ClosePort(4001); err != nil || !closed {
		t.Fatalf("ClosePort = %v, %v", closed, err)
	}
	if _, found := forwards.Get(owners.ID); found {
		t.Fatal("the closed forward is still listed")
	}
	if closed, err := forwards.ClosePort(4001); err != nil || closed {
		t.Fatalf("ClosePort twice = %v, %v", closed, err)
	}
	file, err := os.ReadFile(filepath.Join(directory, "forwards.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]map[string]any
	if err := json.Unmarshal(file, &stored); err != nil {
		t.Fatal(err)
	}
	if _, public := stored[api.ID]["public"]; public {
		t.Fatalf("a private forward stores public: %v", stored[api.ID])
	}
	if info, err := os.Stat(filepath.Join(directory, "forwards.json")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("forwards.json mode = %v, %v", info, err)
	}
	if err := os.WriteFile(filepath.Join(directory, "forwards.json"), []byte(`{"Bad-ID":{"port":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = OpenForwards(directory)
	wantError(t, err, `Invalid forwards.json`)
}

func TestForwardsFileFormatIsPis(t *testing.T) {
	directory := t.TempDir()
	forwards := reopened(t, directory)
	forward, err := forwards.Add(5173, text("web"), nil, true, "s1")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(filepath.Join(directory, "forwards.json"))
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n\t\"" + forward.ID + "\": {\n\t\t\"port\": 5173,\n\t\t\"name\": \"web\",\n\t\t\"public\": true,\n\t\t\"session\": \"s1\"\n\t}\n}\n"
	if string(file) != want {
		t.Fatalf("forwards.json = %q, want %q", file, want)
	}
	if _, err := os.Stat(filepath.Join(directory, "forwards.json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the temporary file stays: %v", err)
	}
}

func TestInvalidForwardsFilesAreRefusedLikePis(t *testing.T) {
	for name, content := range map[string]string{
		"not an object":    `[]`,
		"null":             `null`,
		"entry not object": `{"web":5}`,
		"public not bool":  `{"web":{"port":1,"public":"yes"}}`,
		"empty session":    `{"web":{"port":1,"session":""}}`,
		"session not text": `{"web":{"port":1,"session":3}}`,
		"port missing":     `{"web":{}}`,
		"port fraction":    `{"web":{"port":1.5}}`,
		"port too large":   `{"web":{"port":65536}}`,
		"name not text":    `{"web":{"port":1,"name":null}}`,
		"name too long":    `{"web":{"port":1,"name":"` + strings.Repeat("x", 41) + `"}}`,
		"id too long":      `{"` + strings.Repeat("a", 25) + `":{"port":1}}`,
		"not JSON":         `{`,
	} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "forwards.json"), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenForwards(directory); err == nil {
			t.Errorf("%s: forwards.json %s was accepted", name, content)
		}
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "forwards.json"), []byte(`{"web":{"port":8080,"name":"  api  ","public":true,"session":"s"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	forwards := reopened(t, directory)
	if forward, found := forwards.Get("web"); !found || forward != (Forward{ID: "web", Port: 8080, Name: "api", Public: true, Session: "s"}) {
		t.Fatalf("web = %+v, %v", forward, found)
	}
}

func TestForwardsTellTheirListenersOfEveryChange(t *testing.T) {
	forwards := reopened(t, t.TempDir())
	var told atomic.Int32
	stop := forwards.Subscribe(func() { told.Add(1) })
	forward, err := forwards.Add(3000, nil, nil, false, "")
	if err != nil {
		t.Fatal(err)
	}
	if told.Load() != 1 {
		t.Fatalf("told %d times after an add", told.Load())
	}
	origin := text("https://0123456789abcdef0123456789abcdef.rdtun.com")
	forwards.SetRadiusOrigin(origin)
	forwards.SetRadiusOrigin(text(*origin))
	if told.Load() != 2 {
		t.Fatalf("told %d times after the origin was set twice", told.Load())
	}
	if err := forwards.Remove(forward.ID); err != nil {
		t.Fatal(err)
	}
	stop()
	if _, err := forwards.Add(3001, nil, nil, false, ""); err != nil {
		t.Fatal(err)
	}
	if told.Load() != 3 {
		t.Fatalf("a stopped listener was told: %d", told.Load())
	}
}

func TestForwardsStayAtMostFiftyAndConcurrentWritesLoseNothing(t *testing.T) {
	directory := t.TempDir()
	forwards := reopened(t, directory)
	var group sync.WaitGroup
	for port := 1; port <= 60; port++ {
		group.Add(1)
		go func() { defer group.Done(); _, _ = forwards.Add(port, nil, nil, false, "") }()
	}
	group.Wait()
	if got := len(forwards.List()); got != 50 {
		t.Fatalf("forwards = %d, want 50", got)
	}
	if got := len(reopened(t, directory).List()); got != 50 {
		t.Fatalf("persisted forwards = %d, want 50", got)
	}
	_, err := forwards.Add(61, nil, nil, false, "")
	wantError(t, err, `At most 50 forwards`)
}

func TestForwardOriginsAreIsolatedBesideTheBrain(t *testing.T) {
	const origin = "https://0123456789abcdef0123456789abcdef.rdtun.com"
	for _, test := range []struct {
		origin, id string
		public     bool
		want       string
		ok         bool
	}{
		{origin, "web1", false, "https://fwd--0123456789abcdef0123456789abcdef--web1.rdtun.com", true},
		{origin, "web1", true, "https://pub--0123456789abcdef0123456789abcdef--web1.rdtun.com", true},
		{"http://0123456789abcdef0123456789abcdef.rdtun.localhost:8791", "web1", false, "http://fwd--0123456789abcdef0123456789abcdef--web1.rdtun.localhost:8791", true},
		{"https://tunnel.example", "web1", false, "", false},
		{origin, "Bad-ID", false, "", false},
	} {
		got, ok := ForwardOrigin(test.origin, test.id, test.public)
		if got != test.want || ok != test.ok {
			t.Errorf("ForwardOrigin(%q, %q, %v) = %q, %v", test.origin, test.id, test.public, got, ok)
		}
	}
}
