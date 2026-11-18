package codingagent

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/ai"
	"github.com/MichaelKinsy/PiG/tui"
)

func TestMaskSecretInputSettingsRoundTrip(t *testing.T) {
	item := settingsSelectorRow(t, SettingsConfig{MaskSecretInput: true}, "mask-secret-input")
	if item.Label != "Mask secret input" || item.CurrentValue != "true" || !strings.Contains(item.Description, "Pi") || !strings.Contains(item.Description, "false") {
		t.Fatalf("setting metadata = %+v", item)
	}
	for _, value := range []string{"false", "true"} {
		sm := NewSettingsManager(t.TempDir(), t.TempDir())
		var reported []bool
		list := NewSettingsSelectorComponent(SettingsConfig{MaskSecretInput: value == "false"}, SettingsCallbacks{OnMaskSecretInputChange: func(enabled bool) {
			reported = append(reported, enabled)
			if err := sm.SetMaskSecretInput(enabled); err != nil {
				t.Fatal(err)
			}
		}}).GetSettingsList()
		list.SelectItem("mask-secret-input")
		list.HandleInput("\r")
		if len(reported) != 1 || strconv.FormatBool(reported[0]) != value {
			t.Fatalf("cycling reported %v, want [%s]", reported, value)
		}
		s := sm.Get()
		encoded, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(encoded), `"maskSecretInput":`+value) {
			t.Fatalf("serialized settings = %s", encoded)
		}
		var restored Settings
		if err := json.Unmarshal(encoded, &restored); err != nil {
			t.Fatal(err)
		}
		if strconv.FormatBool(restored.GetMaskSecretInput()) != value {
			t.Fatal("setting did not round trip")
		}
		sm.Reload()
		if strconv.FormatBool(sm.Get().GetMaskSecretInput()) != value {
			t.Fatal("setting was not persisted")
		}
	}
}

func TestMaskSecretInputSettingsMenuAppliesToNextDialog(t *testing.T) {
	m := newPostLoginTestMode(t)
	sc := m.buildSlashContext(t.Context())
	sc.ShowSettingsSelector = func(build func(done func()) *SettingsSelectorComponent) {
		list := build(func() {}).GetSettingsList()
		list.SelectItem("mask-secret-input")
		list.HandleInput("\r")
		for _, item := range list.Items() {
			if item.ID == "mask-secret-input" && item.CurrentValue != "false" {
				t.Errorf("menu shows %q after the change, want false", item.CurrentValue)
			}
		}
	}
	if err := settingsHandler(sc); err != nil {
		t.Fatal(err)
	}
	d := m.newLoginDialog("Test", nil)
	d.ShowSecretInput("API key", "")
	d.HandleInput("plain-after-toggle")
	if !strings.Contains(plainRender(d), "plain-after-toggle") {
		t.Fatal("new login ignored /settings change")
	}
}

func TestMaskedLoginErrorDoesNotEnterFramesOrSession(t *testing.T) {
	m := newPostLoginTestMode(t)
	inner, err := m.newSessionManager().Create("secret-diagnostic", "")
	if err != nil {
		t.Fatal(err)
	}
	m.opts.SessionHandle = &recordingCompactHandle{agent: m.agent, inner: inner}
	const secret = "synthetic-private-key-abcd"
	m.opts.ModelBuilder = func(string) (*ai.Model, error) { return nil, errors.New("server echoed " + secret) }
	if err := setPostLoginAPIKey(m, "openai", secret); err != nil {
		t.Fatal(err)
	}
	waitPostLoginStatus(t, m, "selecting its default model failed")
	frame := plainRender(m.chatContainer)
	if strings.Contains(frame, secret) || !strings.Contains(frame, "••••••••abcd") {
		t.Fatal("authentication error was not redacted")
	}
	data, err := json.Marshal(inner.GetEntries())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatal("login secret entered Session entries")
	}
	// Credentials intentionally belong in auth.json, not in diagnostic/session output.
	if err := filepath.WalkDir(m.opts.AgentDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Name() == "auth.json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(data), secret) {
			t.Errorf("secret leaked outside credential storage: %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPiIgnoresMaskSecretInputInSharedSettings(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []bool{true, false} {
		dir := t.TempDir()
		// Exercise the integration's shared lock backend as well as the extra JSON key.
		sm := NewSettingsManager(t.TempDir(), dir)
		if err := sm.UpdateGlobal(func(s *Settings) { s.MaskSecretInput = &value }); err != nil {
			t.Fatal(err)
		}
		data, err := exec.CommandContext(t.Context(), "node", filepath.Join(root, "test/parity/testdata/login-dialog-privacy.mjs"), root, "settings", dir).CombinedOutput()
		if err != nil {
			t.Fatalf("Pi settings reader: %v: %s", err, data)
		}
		var result struct {
			Value  bool
			Errors []any
			Theme  string
		}
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.Value != value || len(result.Errors) != 0 || result.Theme != "light" {
			t.Fatalf("Pi rejected or misused shared setting: %s", data)
		}
		sm.Reload()
		if sm.Get().GetMaskSecretInput() != value || sm.Get().Theme != "light" {
			t.Fatal("Pi's settings update lost the Pig-only preference")
		}
	}
}

func TestLoginMaskSettingReachesStandardDialog(t *testing.T) {
	m := newPostLoginTestMode(t)
	m.layout = tui.NewContainer(m.chatContainer, m.editorContainer)
	if err := os.WriteFile(filepath.Join(m.opts.AgentDir, "settings.json"), []byte(`{"maskSecretInput":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m.opts.SettingsManager.Reload()
	done := make(chan error, 1)
	go func() { done <- m.runAPIKeyLogin(tui.OAuthProvider{ID: "openai", Name: "OpenAI", AuthType: "api_key"}) }()
	waitForRender(t, m.editorContainer, "Enter OpenAI API key")
	const key = "visible-like-pi"
	deliverModalInput(t, m, []byte(key))
	deliverModalInput(t, m, []byte(""))
	frame := plainRender(m.editorContainer)
	deliverModalInput(t, m, []byte("\r"))
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	store, err := ai.NewAuthStorage(filepath.Join(m.opts.AgentDir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	credential, ok, err := store.Get("openai")
	if err != nil || !ok || credential.Key != key {
		t.Fatal("changed submitted value")
	}
	if !strings.Contains(frame, key) || strings.Contains(frame, "Input hidden") {
		t.Fatal("false did not restore Pi input rendering")
	}
}

const piSettingsSelectorSource = "../../.upstream/current/packages/coding-agent/src/modes/interactive/components/settings-selector.ts"

var (
	piSettingsArrayRow  = regexp.MustCompile(`(?m)^\t\t\t\tid: "([^"]+)",\n\t\t\t\tlabel: "([^"]+)",$`)
	piSettingsFindIndex = regexp.MustCompile(`^const (\w+) = items\.findIndex\(\(item\) => item\.id === "([^"]+)"\);$`)
	piSettingsSplice    = regexp.MustCompile(`^items\.splice\((.+), 0, \{$`)
	piSettingsTernary   = regexp.MustCompile(`^supportsImages \? (\d+) : (\d+)$`)
	piSettingsAfter     = regexp.MustCompile(`^(\w+) \+ 1$`)
	piSettingsID        = regexp.MustCompile(`^id: "([^"]+)",$`)
	piSettingsLabel     = regexp.MustCompile(`^label: "([^"]+)",$`)
	piSettingsItemsUse  = regexp.MustCompile(`\bitems\s*[.\[=]`)
)

// piSettingsRoster replays the item list of Pi's SettingsSelectorComponent constructor from its source: the rows of the `items` array literal, then every `items.splice` in statement order, with the `if (supportsImages)` block taken only when supportsImages is set. Each row is "id (label)". A statement on `items` or a condition it does not know fails the test, so a new way of adding or gating a row cannot pass unseen.
func piSettingsRoster(t *testing.T, source string, supportsImages bool) []string {
	t.Helper()
	_, body, ok := strings.Cut(source, "\t\tconst supportsImages = getCapabilities().images;\n")
	if !ok {
		t.Fatal("settings-selector.ts: no supportsImages declaration")
	}
	if body, _, ok = strings.Cut(body, "\t\tthis.settingsList = new SettingsList("); !ok {
		t.Fatal("settings-selector.ts: no SettingsList construction")
	}
	_, body, ok = strings.Cut(body, "\t\tconst items: SettingItem[] = [\n")
	if !ok {
		t.Fatal("settings-selector.ts: no items array")
	}
	array, rest, ok := strings.Cut(body, "\n\t\t];\n")
	if !ok {
		t.Fatal("settings-selector.ts: items array is not closed")
	}
	var roster []string
	for _, m := range piSettingsArrayRow.FindAllStringSubmatch(array, -1) {
		roster = append(roster, m[1]+" ("+m[2]+")")
	}
	if entries := strings.Count(array, "\n\t\t\t\tid: "); len(roster) == 0 || len(roster) != entries {
		t.Fatalf("settings-selector.ts: read %d of %d items array rows", len(roster), entries)
	}
	indexOf := func(id string) int {
		return slices.IndexFunc(roster, func(row string) bool { return strings.HasPrefix(row, id+" (") })
	}
	indexes := map[string]int{}
	inImages := false
	lines := strings.Split(rest, "\n")
	for i := 0; i < len(lines); i++ { // the body skips the two lines of a spliced row
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "//") {
			continue
		}
		if lines[i] == "\t\tif (supportsImages) {" {
			inImages = true
			continue
		}
		if inImages && lines[i] == "\t\t}" {
			inImages = false
			continue
		}
		if m := piSettingsFindIndex.FindStringSubmatch(line); m != nil {
			indexes[m[1]] = indexOf(m[2])
			continue
		}
		m := piSettingsSplice.FindStringSubmatch(line)
		if m == nil {
			if piSettingsItemsUse.MatchString(line) || strings.HasPrefix(line, "if ") || strings.HasPrefix(line, "} else") {
				t.Fatalf("settings-selector.ts: statement not understood by the roster reader: %s", line)
			}
			continue
		}
		at := 0
		if n, err := strconv.Atoi(m[1]); err == nil {
			at = n
		} else if c := piSettingsTernary.FindStringSubmatch(m[1]); c != nil {
			at, _ = strconv.Atoi(c[2])
			if supportsImages {
				at, _ = strconv.Atoi(c[1])
			}
		} else if a := piSettingsAfter.FindStringSubmatch(m[1]); a != nil {
			index, known := indexes[a[1]]
			if !known {
				t.Fatalf("settings-selector.ts: splice at unknown index %s", a[1])
			}
			at = index + 1
		} else {
			t.Fatalf("settings-selector.ts: splice position not understood: %s", m[1])
		}
		if i+2 >= len(lines) {
			t.Fatalf("settings-selector.ts: splice without a row: %s", line)
		}
		id := piSettingsID.FindStringSubmatch(strings.TrimSpace(lines[i+1]))
		label := piSettingsLabel.FindStringSubmatch(strings.TrimSpace(lines[i+2]))
		if id == nil || label == nil {
			t.Fatalf("settings-selector.ts: splice without a literal id and label: %s", line)
		}
		i += 2
		if inImages && !supportsImages {
			continue
		}
		roster = slices.Insert(roster, min(at, len(roster)), id[1]+" ("+label[1]+")")
	}
	return roster
}

// pigSettingsRosterFromPi applies the two recorded differences to Pi's roster: D64 omits "share-destination" and D80 adds "mask-secret-input" after "tree-filter-mode". It reports whether both anchors were present in Pi's roster.
func pigSettingsRosterFromPi(pi []string) (want []string, anchored bool) {
	omitted, added := false, false
	for _, row := range pi {
		if strings.HasPrefix(row, "share-destination (") {
			omitted = true // D64
			continue
		}
		want = append(want, row)
		if strings.HasPrefix(row, "tree-filter-mode (") {
			want = append(want, "mask-secret-input (Mask secret input)") // D80
			added = true
		}
	}
	return want, omitted && added
}

// The denominator is the pinned Pi source, not a typed list: on a terminal with and without inline images PiG's /settings rows are Pi's rows, ids and labels in order, minus "share-destination" (D64) plus "mask-secret-input" (D80), so both list counters read the same total. A pin that adds, removes, renames or re-gates a Pi row turns this red.
func TestSettingsRosterDiffersFromPiOnlyByD64AndD80(t *testing.T) {
	raw, err := os.ReadFile(piSettingsSelectorSource)
	if err != nil {
		t.Fatal(err)
	}
	prev := tuiCapabilitiesForTest()
	defer prev.restore()
	for _, images := range []bool{false, true} {
		pi := piSettingsRoster(t, string(raw), images)
		want, anchored := pigSettingsRosterFromPi(pi)
		// A build that strips a built-in does not offer its rows (settingStripped).
		want, pi = withoutStrippedSettingsRows(want), withoutStrippedSettingsRows(pi)
		if !anchored {
			t.Fatalf("images=%v: Pi roster lacks share-destination or tree-filter-mode: %v", images, pi)
		}
		tuiSetCapsForTest(images)
		var got []string
		for _, item := range NewSettingsSelectorComponent(SettingsConfig{}, SettingsCallbacks{}).GetSettingsList().Items() {
			got = append(got, item.ID+" ("+item.Label+")")
		}
		if !slices.Equal(got, want) {
			t.Fatalf("images=%v roster\n got  %q\n want %q", images, got, want)
		}
		if len(got) != len(pi) {
			t.Fatalf("images=%v counts: pig %d, pi %d; D64 and D80 must cancel", images, len(got), len(pi))
		}
	}
}

// The roster reader must see a row however Pi adds it: in the array, by a splice after a named row, or behind the image gate.
func TestPiSettingsRosterReadsEveryFormOfRow(t *testing.T) {
	raw, err := os.ReadFile(piSettingsSelectorSource)
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	base := piSettingsRoster(t, source, true)
	mutate := func(old, replacement string) string {
		t.Helper()
		if strings.Count(source, old) != 1 {
			t.Fatalf("settings-selector.ts: %q occurs %d times", old, strings.Count(source, old))
		}
		return strings.Replace(source, old, replacement, 1)
	}
	arrayRow := "\t\t\t{\n\t\t\t\tid: \"transport\",\n"
	inArray := piSettingsRoster(t, mutate(arrayRow, "\t\t\t{\n\t\t\t\tid: \"new-array-row\",\n\t\t\t\tlabel: \"New array row\",\n\t\t\t},\n"+arrayRow), true)
	transport := slices.Index(base, "transport (Transport)")
	if transport < 0 || !slices.Equal(inArray, slices.Insert(slices.Clone(base), transport, "new-array-row (New array row)")) {
		t.Fatalf("array row not read:\n %q", inArray)
	}
	construction := "\t\t// Add borders\n"
	spliced := piSettingsRoster(t, mutate(construction, "\t\tconst themeIndex = items.findIndex((item) => item.id === \"theme\");\n\t\titems.splice(themeIndex + 1, 0, {\n\t\t\tid: \"new-spliced-row\",\n\t\t\tlabel: \"New spliced row\",\n\t\t});\n"+construction), true)
	if !slices.Equal(spliced, append(slices.Clone(base), "new-spliced-row (New spliced row)")) {
		t.Fatalf("spliced row not read:\n %q", spliced)
	}
	withImages, withoutImages := len(base), len(piSettingsRoster(t, source, false))
	if withImages-withoutImages != 2 || !slices.Contains(base, "show-images (Show images)") || !slices.Contains(base, "image-width-cells (Image width)") {
		t.Fatalf("image gate: %d rows with images, %d without", withImages, withoutImages)
	}
}
