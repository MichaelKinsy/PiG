package piglet

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// copiesRun runs one `pig piglet` command and returns its exit code and streams.
func copiesRun(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := RunCommand(append([]string{"piglet"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// copiesTree returns every file below root by slash-separated path.
func copiesTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(relative)] = string(data)
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return files
}

func copiesYAML(name, version, promptFile string) string {
	return "name: " + name + "\nrelease:\n  version: " + version + "\nsystemPrompt:\n  file: " + promptFile + "\ntools: [read]\nextensions: []\nskills: []\ndiscovery:\n  extensions: []\n  skills: []\n"
}

func copiesRequireOK(t *testing.T, args ...string) string {
	t.Helper()
	code, stdout, stderr := copiesRun(t, args...)
	if code != 0 {
		t.Fatalf("pig piglet %v: code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
	}
	return stdout
}

// Issue #417: removing a source Piglet removed its YAML but left the prompt PiG copied, and an edited re-add of the same
// Piglet then failed on that leftover. The sequence is the report's script.
func TestRemoveSourceDeletesCopiedPromptAndEditedReAddSucceeds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	prompt := filepath.Join(work, "prompt.md")
	first, second := filepath.Join(work, "first.yaml"), filepath.Join(work, "second.yaml")
	writeFile(t, first, copiesYAML("file-prompt-repro", "1.0.0", "./prompt.md"))
	writeFile(t, prompt, "Synthetic assistant.\nReply briefly.\n")

	copiesRequireOK(t, "add", first, "--json")
	registered := copiesTree(t, filepath.Join(home, "piglets"))
	if registered["prompt.md"] != "Synthetic assistant.\nReply briefly.\n" || registered["file-prompt-repro.yaml"] == "" || registered["file-prompt-repro.files.json"] == "" {
		t.Fatalf("registration files = %v", registered)
	}
	copiesRequireOK(t, "remove", "file-prompt-repro", "--source", "--json")
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
		t.Fatalf("files left after remove --source: %v", left)
	}

	writeFile(t, second, copiesYAML("file-prompt-repro", "1.0.1", "./prompt.md"))
	writeFile(t, prompt, "Synthetic assistant.\nReply in bullet points.\nUse a neutral tone.\n")
	copiesRequireOK(t, "validate", second, "--json", "--no-input")
	copiesRequireOK(t, "add", second, "--json")

	cleanHome := t.TempDir()
	t.Setenv("PIG_HOME", cleanHome)
	copiesRequireOK(t, "add", second, "--json")
	got, want := copiesTree(t, filepath.Join(home, "piglets")), copiesTree(t, filepath.Join(cleanHome, "piglets"))
	if len(got) != len(want) {
		t.Fatalf("replaced home files = %v, fresh home files = %v", got, want)
	}
	for path, content := range want {
		if got[path] != content {
			t.Fatalf("%s = %q, fresh home has %q", path, got[path], content)
		}
	}
}

func TestRemoveSourceKeepsPromptAnotherPigletUses(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "shared\n")
	writeFile(t, filepath.Join(work, "alpha.yaml"), copiesYAML("alpha", "1.0.0", "./prompt.md"))
	writeFile(t, filepath.Join(work, "beta.yaml"), copiesYAML("beta", "1.0.0", "prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "alpha.yaml"))
	copiesRequireOK(t, "add", filepath.Join(work, "beta.yaml"))

	stdout := copiesRequireOK(t, "remove", "alpha", "--source")
	prompt := filepath.Join(home, "piglets", "prompt.md")
	if data, err := os.ReadFile(prompt); err != nil || string(data) != "shared\n" {
		t.Fatalf("shared prompt after removing alpha: data=%q err=%v", data, err)
	}
	if !strings.Contains(stdout, "kept "+prompt+": Piglet beta also uses it") || strings.Contains(stdout, "removed copied file") {
		t.Fatalf("remove alpha output = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "alpha.files.json")); !os.IsNotExist(err) {
		t.Fatalf("alpha copied-file record survived: %v", err)
	}

	stdout = copiesRequireOK(t, "remove", "beta", "--source")
	if !strings.Contains(stdout, "removed copied file: "+prompt) {
		t.Fatalf("remove beta output = %q", stdout)
	}
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
		t.Fatalf("files left after removing both: %v", left)
	}
}

// A registration made before PiG recorded copied files has no record; another Piglet's reference alone keeps the file.
func TestRemoveSourceKeepsPromptAnUnrecordedPigletReferences(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "shared\n")
	writeFile(t, filepath.Join(work, "alpha.yaml"), copiesYAML("alpha", "1.0.0", "./prompt.md"))
	writeFile(t, filepath.Join(work, "beta.yaml"), copiesYAML("beta", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "alpha.yaml"))
	copiesRequireOK(t, "add", filepath.Join(work, "beta.yaml"))
	if err := os.Remove(filepath.Join(home, "piglets", "beta.files.json")); err != nil {
		t.Fatal(err)
	}
	stdout := copiesRequireOK(t, "remove", "alpha", "--source")
	if !strings.Contains(stdout, "Piglet beta also uses it") {
		t.Fatalf("output = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompt.md")); err != nil {
		t.Fatalf("prompt referenced by beta was deleted: %v", err)
	}
}

func TestRemoveSourceKeepsEditedCopyAndSaysSo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "original\n")
	writeFile(t, filepath.Join(work, "edited.yaml"), copiesYAML("edited", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "edited.yaml"))
	edited := filepath.Join(home, "piglets", "prompt.md")
	writeFile(t, edited, "edited by the user\n")

	stdout := copiesRequireOK(t, "remove", "edited", "--source")
	if !strings.Contains(stdout, "kept "+edited+": it changed after PiG copied it") || strings.Contains(stdout, "removed copied file") {
		t.Fatalf("output = %q", stdout)
	}
	left := copiesTree(t, filepath.Join(home, "piglets"))
	if len(left) != 1 || left["prompt.md"] != "edited by the user\n" {
		t.Fatalf("files left = %v, want only the edited prompt", left)
	}

	// Nothing owns the kept file now, so a re-add with different content refuses and says what to do.
	writeFile(t, filepath.Join(work, "prompt.md"), "replacement\n")
	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "edited.yaml"))
	if code != 1 || !strings.Contains(stderr, "already exists with different content") || !strings.Contains(stderr, "no registered Piglet copied it") || !strings.Contains(stderr, "delete that file yourself") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if data, _ := os.ReadFile(edited); string(data) != "edited by the user\n" {
		t.Fatalf("add overwrote the edited file: %q", data)
	}
}

// What a 0.7.0 home holds after `remove --source`: a prompt no record owns. Add must not overwrite it.
func TestAddRefusesUnrecordedLeftoverPromptAndNamesTheFix(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(home, "piglets", "prompt.md"), "old\n")
	writeFile(t, filepath.Join(work, "prompt.md"), "new\n")
	writeFile(t, filepath.Join(work, "leftover.yaml"), copiesYAML("leftover", "1.0.1", "./prompt.md"))
	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "leftover.yaml"))
	if code != 1 || !strings.Contains(stderr, "no registered Piglet copied it, so PiG will not delete it") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "piglets", "prompt.md")); string(data) != "old\n" {
		t.Fatalf("leftover prompt was overwritten: %q", data)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "leftover.yaml")); !os.IsNotExist(err) {
		t.Fatalf("failed add left a registration: %v", err)
	}
}

func TestAddRefusesPromptAnotherPigletCopiedAndNamesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	one, two := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(one, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(one, "alpha.yaml"), copiesYAML("alpha", "1.0.0", "./prompt.md"))
	writeFile(t, filepath.Join(two, "prompt.md"), "two\n")
	writeFile(t, filepath.Join(two, "beta.yaml"), copiesYAML("beta", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(one, "alpha.yaml"))
	code, _, stderr := copiesRun(t, "add", filepath.Join(two, "beta.yaml"))
	if code != 1 || !strings.Contains(stderr, "Piglet alpha uses it; run `pig piglet remove alpha --source` first") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if data, _ := os.ReadFile(filepath.Join(home, "piglets", "prompt.md")); string(data) != "one\n" {
		t.Fatalf("alpha's prompt was overwritten: %q", data)
	}
}

func TestAddWithRegisteredNameNamesTheRemoveCommand(t *testing.T) {
	t.Setenv("PIG_HOME", t.TempDir())
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(work, "same.yaml"), copiesYAML("same", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "same.yaml"))
	writeFile(t, filepath.Join(work, "same.yaml"), copiesYAML("same", "1.0.1", "./prompt.md"))
	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "same.yaml"))
	if code != 1 || !strings.Contains(stderr, "Piglet \"same\" is already registered; run `pig piglet remove same --source`") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestAddIdenticalSourceTwiceStaysIdempotent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(work, "same.yaml"), copiesYAML("same", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "same.yaml"))
	before := copiesTree(t, filepath.Join(home, "piglets"))
	copiesRequireOK(t, "add", filepath.Join(work, "same.yaml"))
	after := copiesTree(t, filepath.Join(home, "piglets"))
	if len(before) != len(after) {
		t.Fatalf("before=%v after=%v", before, after)
	}
	for path, content := range before {
		if after[path] != content {
			t.Fatalf("%s changed on identical re-add", path)
		}
	}
}

// A registration whose YAML was deleted by hand still has its record; add reclaims the files it owned.
func TestAddReplacesFilesOfOrphanedRegistrationOfTheSameName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(work, "gone.yaml"), copiesYAML("gone", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "gone.yaml"))
	if err := os.Remove(filepath.Join(home, "piglets", "gone.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(work, "prompt.md"), "two\n")
	stdout := copiesRequireOK(t, "add", filepath.Join(work, "gone.yaml"))
	if !strings.Contains(stdout, "removed copied file: "+filepath.Join(home, "piglets", "prompt.md")) || !strings.Contains(stdout, "Added gone") {
		t.Fatalf("output = %q", stdout)
	}
	if got := copiesTree(t, filepath.Join(home, "piglets"))["prompt.md"]; got != "two\n" {
		t.Fatalf("prompt = %q, want the replacement", got)
	}

	// An orphan whose copy was edited keeps that copy, so add refuses instead of overwriting it.
	if err := os.Remove(filepath.Join(home, "piglets", "gone.yaml")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(home, "piglets", "prompt.md"), "user edit\n")
	writeFile(t, filepath.Join(work, "prompt.md"), "three\n")
	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "gone.yaml"))
	if code != 1 || !strings.Contains(stderr, "already exists with different content") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if got := copiesTree(t, filepath.Join(home, "piglets"))["prompt.md"]; got != "user edit\n" {
		t.Fatalf("prompt = %q, want the user's edit", got)
	}
}

func TestRemoveSourceDeletesNestedCopiesAndTheirEmptyDirectories(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompts", "deep", "system.md"), "nested\n")
	writeFile(t, filepath.Join(work, "nested.yaml"), copiesYAML("nested", "1.0.0", "prompts/deep/system.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "nested.yaml"))
	var record pigletCopies
	data, err := os.ReadFile(filepath.Join(home, "piglets", "nested.files.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatal(err)
	}
	if _, ok := record.Files["prompts/deep/system.md"]; !ok || len(record.Files) != 1 {
		t.Fatalf("record keys = %v, want the slash-separated relative path on every platform", record.Files)
	}
	copiesRequireOK(t, "remove", "nested", "--source")
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompts")); !os.IsNotExist(err) {
		t.Fatalf("empty copied directory survived: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets")); err != nil {
		t.Fatalf("Piglet directory itself was removed: %v", err)
	}
}

func TestRemoveSourceJSONReportsRemovedAndKeptCopies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "original\n")
	writeFile(t, filepath.Join(work, "json.yaml"), copiesYAML("json", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "json.yaml"))
	writeFile(t, filepath.Join(home, "piglets", "prompt.md"), "edited\n")

	stdout := copiesRequireOK(t, "remove", "json", "--source", "--json", "--no-input")
	assertNoPigletOutputVersion(t, stdout)
	var output pigletCommandOutput
	if err := json.Unmarshal([]byte(stdout), &output); err != nil {
		t.Fatal(err)
	}
	prompt := filepath.Join(home, "piglets", "prompt.md")
	for _, want := range []string{
		"removed source: " + filepath.Join(home, "piglets", "json.yaml"),
		"removed copied-file record: " + filepath.Join(home, "piglets", "json.files.json"),
		"kept " + prompt + ": it changed after PiG copied it",
	} {
		if !strings.Contains(output.Output, want) {
			t.Fatalf("JSON output missing %q: %#v", want, output)
		}
	}
	if output.Command != "remove" || !output.Success || output.Error != "" {
		t.Fatalf("output = %#v", output)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatal(err)
	}
	for key := range document {
		if key != "command" && key != "success" && key != "output" && key != "error" {
			t.Fatalf("unexpected JSON key %q in %s", key, stdout)
		}
	}
}

func TestPigletCopiesRejectsWhatTheRecordCannotSafelyName(t *testing.T) {
	digest := digestPigletData([]byte("x"))
	for name, files := range map[string]map[string]string{
		"empty":              {},
		"parent":             {"../prompt.md": digest},
		"nested parent":      {"prompts/../../prompt.md": digest},
		"dot prefix":         {"./prompt.md": digest},
		"absolute":           {"/etc/passwd": digest},
		"digest not sha256":  {"prompt.md": "md5:00"},
		"uppercase digest":   {"prompt.md": "sha256:" + strings.ToUpper(strings.TrimPrefix(digest, "sha256:"))},
		"missing digest":     {"prompt.md": ""},
		"directory itself":   {".": digest},
		"trailing separator": {"prompts/": digest},
		"nul":                {"prompt\x00.md": digest},
	} {
		if err := (pigletCopies{Files: files}).validate(); err == nil {
			t.Errorf("%s: record with %v validated", name, files)
		}
	}
	// A name this platform would read as a drive or another separator is refused where that is true.
	windows := runtime.GOOS == "windows"
	for _, key := range []string{`prompts\system.md`, "C:/prompt.md", `..\prompt.md`} {
		if err := (pigletCopies{Files: map[string]string{key: digest}}).validate(); windows && err == nil {
			t.Errorf("%q validated on Windows", key)
		}
	}
	for _, key := range []string{"prompts/system.md", "~/prompt.md", ".git/config", "prompts/a b.md"} {
		if err := (pigletCopies{Files: map[string]string{key: digest}}).validate(); err != nil {
			t.Errorf("%q rejected: %v", key, err)
		}
	}
}

func TestReadPigletCopiesIsStrict(t *testing.T) {
	digest := digestPigletData([]byte("x"))
	path := filepath.Join(t.TempDir(), "p.files.json")
	for name, body := range map[string]string{
		"unknown field": `{"files":{"prompt.md":"` + digest + `"},"version":1}`,
		"trailing":      `{"files":{"prompt.md":"` + digest + `"}} {}`,
		"not an object": `[]`,
	} {
		writeFile(t, path, body)
		if _, err := readPigletCopies(path); err == nil {
			t.Errorf("%s: record accepted", name)
		}
	}
	writeFile(t, path, `{"files":{"prompt.md":"`+digest+`"}}`)
	if copies, err := readPigletCopies(path); err != nil || copies == nil || copies.Files["prompt.md"] != digest {
		t.Fatalf("copies=%v err=%v", copies, err)
	}
	if copies, err := readPigletCopies(filepath.Join(t.TempDir(), "absent.files.json")); err != nil || copies != nil {
		t.Fatalf("absent record: copies=%v err=%v", copies, err)
	}
}

// A corrupt record fails removal before anything is deleted, rather than guessing what PiG copied.
func TestRemoveSourceFailsClosedOnACorruptCopiedFileRecord(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "original\n")
	writeFile(t, filepath.Join(work, "corrupt.yaml"), copiesYAML("corrupt", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "corrupt.yaml"))
	writeFile(t, filepath.Join(home, "piglets", "corrupt.files.json"), `{"files":{"../../escape":"sha256:00"}}`)
	code, _, stderr := copiesRun(t, "remove", "corrupt", "--source")
	if code != 1 || !strings.Contains(stderr, "copied-file") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 3 {
		t.Fatalf("a failed removal deleted files: %v", left)
	}
}

func TestSameFilePathAsksTheFileSystem(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "Prompt.md"), "x")
	writeFile(t, filepath.Join(dir, "\u00e9.md"), "x")
	writeFile(t, filepath.Join(dir, "other.md"), "x")
	// Whether these spellings name one file is the file system's answer (case folding, Unicode normalization).
	for _, alias := range [][2]string{{"Prompt.md", "prompt.md"}, {"\u00e9.md", "e\u0301.md"}} {
		_, err := os.Stat(filepath.Join(dir, alias[1]))
		if got := sameFilePath(filepath.Join(dir, alias[0]), filepath.Join(dir, alias[1])); got != (err == nil) {
			t.Errorf("sameFilePath(%q, %q) = %v, but the file system resolves the second spelling: %v", alias[0], alias[1], got, err == nil)
		}
	}
	if sameFilePath(filepath.Join(dir, "Prompt.md"), filepath.Join(dir, "other.md")) {
		t.Fatal("different files compare equal")
	}
	if !sameFilePath(filepath.Join(dir, "x", "..", "Prompt.md"), filepath.Join(dir, "Prompt.md")) {
		t.Fatal("unclean spelling of one path compares different")
	}
	if sameFilePath(filepath.Join(dir, "gone-a.md"), filepath.Join(dir, "gone-b.md")) {
		t.Fatal("different missing paths compare equal")
	}
	// A second name for the same file is the same file on every file system that can link.
	if err := os.Link(filepath.Join(dir, "other.md"), filepath.Join(dir, "linked.md")); err == nil {
		if !sameFilePath(filepath.Join(dir, "other.md"), filepath.Join(dir, "linked.md")) {
			t.Fatal("two names of one file compare different")
		}
		if sameFilePath(filepath.Join(dir, "Prompt.md"), filepath.Join(dir, "linked.md")) {
			t.Fatal("a link to another file compares equal")
		}
	}
}

// Two Piglets whose prompt paths differ only in case or normalization share one file where the file system folds them.
func TestRemoveSourceKeepsPromptAnotherPigletSpellsDifferently(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "shared\n")
	if _, err := os.Stat(filepath.Join(work, "PROMPT.md")); err != nil {
		t.Skip("the file system distinguishes case, so the two spellings are two files")
	}
	writeFile(t, filepath.Join(work, "alpha.yaml"), copiesYAML("alpha", "1.0.0", "prompt.md"))
	writeFile(t, filepath.Join(work, "beta.yaml"), copiesYAML("beta", "1.0.0", "PROMPT.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "alpha.yaml"))
	copiesRequireOK(t, "add", filepath.Join(work, "beta.yaml"))
	stdout := copiesRequireOK(t, "remove", "alpha", "--source")
	if !strings.Contains(stdout, "Piglet beta also uses it") {
		t.Fatalf("output = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompt.md")); err != nil {
		t.Fatalf("the file beta uses was deleted: %v", err)
	}
}

// The first conflict add finds can be the registered Piglet's own record, not the prompt (the record sorts before the
// YAML, and before the prompt for the report's name); every one of them names the command that replaces it.
func TestAddOfRegisteredNameNamesTheRemoveCommandWhicheverFileConflictsFirst(t *testing.T) {
	for _, name := range []string{"file-prompt-repro", "zeta"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			work := t.TempDir()
			writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
			writeFile(t, filepath.Join(work, "first.yaml"), copiesYAML(name, "1.0.0", "./prompt.md"))
			copiesRequireOK(t, "add", filepath.Join(work, "first.yaml"))
			writeFile(t, filepath.Join(work, "prompt.md"), "two\n")
			writeFile(t, filepath.Join(work, "second.yaml"), copiesYAML(name, "1.0.1", "./prompt.md"))
			code, _, stderr := copiesRun(t, "add", filepath.Join(work, "second.yaml"))
			if code != 1 || !strings.Contains(stderr, "run `pig piglet remove "+name+" --source`") || strings.Contains(stderr, "no registered Piglet copied it") {
				t.Fatalf("code=%d stderr=%s", code, stderr)
			}
			if got := copiesTree(t, filepath.Join(home, "piglets"))["prompt.md"]; got != "one\n" {
				t.Fatalf("refused add changed the prompt: %q", got)
			}
		})
	}
}

func TestPigletRegistrationNameCoversEveryRegistrationFile(t *testing.T) {
	dir := t.TempDir()
	for _, file := range []string{"a.yaml", "b.yml", "x.y.yaml"} {
		writeFile(t, filepath.Join(dir, file), "name: n\n")
	}
	for file, want := range map[string]string{
		"a.yaml": "a", "b.yml": "b", "gone.yaml": "gone", // the YAML itself, present or not
		"a.files.json": "a", "a.origin.json": "a", "b.files.json": "b", "b.origin.json": "b", "x.y.files.json": "x.y", // records of a registered Piglet
	} {
		if got, ok := pigletRegistrationName(dir, filepath.Join(dir, file)); !ok || got != want {
			t.Errorf("%s: name=%q ok=%v, want %q", file, got, ok, want)
		}
	}
	// A record whose YAML is gone belongs to no registered Piglet: remove has nothing to remove for it.
	for _, target := range []string{
		filepath.Join(dir, "gone.origin.json"), filepath.Join(dir, "gone.files.json"),
		filepath.Join(dir, "prompt.md"), filepath.Join(dir, "prompts", "a.yaml"), filepath.Join(dir, ".yaml"), filepath.Join(dir, ".origin.json"),
	} {
		if name, ok := pigletRegistrationName(dir, target); ok {
			t.Errorf("%s named Piglet %q", target, name)
		}
	}
}

// A copied directory replaced by a link to somewhere else must not make remove delete what the link points at.
func TestRemoveSourceNeverDeletesThroughASymlinkedDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompts", "system.md"), "nested\n")
	writeFile(t, filepath.Join(work, "linked.yaml"), copiesYAML("linked", "1.0.0", "prompts/system.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "linked.yaml"))
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "system.md"), "nested\n")
	if err := os.RemoveAll(filepath.Join(home, "piglets", "prompts")); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, outside, filepath.Join(home, "piglets", "prompts"))

	stdout := copiesRequireOK(t, "remove", "linked", "--source")
	if data, err := os.ReadFile(filepath.Join(outside, "system.md")); err != nil || string(data) != "nested\n" {
		t.Fatalf("remove deleted a file outside the Piglet directory: data=%q err=%v", data, err)
	}
	if !strings.Contains(stdout, "it is no longer a regular file PiG copied") || strings.Contains(stdout, "removed copied file") {
		t.Fatalf("output = %q", stdout)
	}
}

// A prompt that names the registered YAML itself is not a separate copy: recording it would make remove delete the YAML twice.
func TestAddDoesNotRecordAPromptThatIsTheRegisteredYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "self.yaml"), copiesYAML("self", "1.0.0", "./self.yaml"))
	copiesRequireOK(t, "add", filepath.Join(work, "self.yaml"))
	if _, err := os.Stat(filepath.Join(home, "piglets", "self.files.json")); !os.IsNotExist(err) {
		t.Fatalf("a copied-file record was written for the YAML itself: %v", err)
	}
	copiesRequireOK(t, "remove", "self", "--source")
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
		t.Fatalf("files left: %v", left)
	}
}

// A record that lists the registered YAML (written by hand or by an older build) must not fail removal.
func TestRemoveSourceIgnoresARecordedEntryForTheRegistrationItself(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	writeFile(t, filepath.Join(home, "piglets", "self.yaml"), "name: self\n")
	writeFile(t, filepath.Join(home, "piglets", "self.files.json"), `{"files":{"self.yaml":"`+digestPigletData([]byte("name: self\n"))+`"}}`)
	copiesRequireOK(t, "remove", "self", "--source")
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
		t.Fatalf("files left: %v", left)
	}
}

// Only the other Piglet's record lists the file here (its YAML no longer names a prompt file), so the record check alone keeps it.
func TestRemoveSourceKeepsFileAnotherPigletsRecordLists(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "shared\n")
	writeFile(t, filepath.Join(work, "alpha.yaml"), copiesYAML("alpha", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "alpha.yaml"))
	writeFile(t, filepath.Join(home, "piglets", "beta.yaml"), "name: beta\nsystemPrompt:\n  text: inline now\n")
	writeFile(t, filepath.Join(home, "piglets", "beta.files.json"), `{"files":{"prompt.md":"`+digestPigletData([]byte("shared\n"))+`"}}`)
	stdout := copiesRequireOK(t, "remove", "alpha", "--source")
	if !strings.Contains(stdout, "Piglet beta also uses it") {
		t.Fatalf("output = %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompt.md")); err != nil {
		t.Fatalf("a file beta's record lists was deleted: %v", err)
	}
}

// A registration that predates the record owns nothing until its unchanged source is added again; that add records the copy.
func TestAddOfUnchangedUnrecordedRegistrationRecordsTheCopy(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(work, "old.yaml"), copiesYAML("old", "1.0.0", "./prompt.md"))
	copiesRequireOK(t, "add", filepath.Join(work, "old.yaml"))
	if err := os.Remove(filepath.Join(home, "piglets", "old.files.json")); err != nil {
		t.Fatal(err)
	}
	// Registered without a record: remove --source leaves the prompt alone.
	stdout := copiesRequireOK(t, "remove", "old", "--source")
	if strings.Contains(stdout, "removed copied file") {
		t.Fatalf("an unrecorded prompt was deleted: %q", stdout)
	}
	if _, err := os.Stat(filepath.Join(home, "piglets", "prompt.md")); err != nil {
		t.Fatalf("an unrecorded prompt was deleted: %v", err)
	}

	// Adding the unchanged source while still registered records the copy.
	copiesRequireOK(t, "add", filepath.Join(work, "old.yaml"))
	if _, err := os.Stat(filepath.Join(home, "piglets", "old.files.json")); err != nil {
		t.Fatalf("add of the unchanged source wrote no record: %v", err)
	}
	copiesRequireOK(t, "remove", "old", "--source")
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
		t.Fatalf("files left: %v", left)
	}
}

// Version 0.7.0 accepted these prompt names, so add keeps accepting them wherever the platform can create the file.
func TestAddKeepsAcceptingPromptNamesWithColonOrLeadingTilde(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows cannot create a file named with a colon")
	}
	for _, name := range []string{"we:ird.md", "~tilde.md"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("PIG_HOME", home)
			work := t.TempDir()
			writeFile(t, filepath.Join(work, name), "odd name\n")
			writeFile(t, filepath.Join(work, "odd.yaml"), copiesYAML("odd", "1.0.0", "'"+name+"'"))
			copiesRequireOK(t, "add", filepath.Join(work, "odd.yaml"))
			if got := copiesTree(t, filepath.Join(home, "piglets"))[name]; got != "odd name\n" {
				t.Fatalf("copied %q = %q", name, got)
			}
			copiesRequireOK(t, "remove", "odd", "--source")
			if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 0 {
				t.Fatalf("files left: %v", left)
			}
		})
	}
}

func TestAddRefusesPromptNameWindowsCannotCreateWithAClearError(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows reads these names as a drive or separator")
	}
	t.Setenv("PIG_HOME", t.TempDir())
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "x.yaml"), copiesYAML("x", "1.0.0", "'C:prompt.md'"))
	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "x.yaml"))
	if code != 1 || strings.Contains(stderr, "added subdirectory") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

// A local add beside a leftover origin record (its YAML deleted by hand) used to succeed and then break `piglet list`
// and `piglet remove`, because the record no longer matches the new YAML.
func TestLocalAddRefusesALeftoverOriginRecordAndNamesIt(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PIG_HOME", home)
	work := t.TempDir()
	writeFile(t, filepath.Join(work, "prompt.md"), "one\n")
	writeFile(t, filepath.Join(work, "left.yaml"), copiesYAML("left", "1.0.0", "./prompt.md"))
	origin := filepath.Join(home, "piglets", "left.origin.json")
	writeFile(t, origin, "{}\n")

	code, _, stderr := copiesRun(t, "add", filepath.Join(work, "left.yaml"))
	if code != 1 || !strings.Contains(stderr, origin) || !strings.Contains(stderr, "origin record of a Piglet whose source is gone") || strings.Contains(stderr, "pig piglet remove") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	if left := copiesTree(t, filepath.Join(home, "piglets")); len(left) != 1 || left["left.origin.json"] != "{}\n" {
		t.Fatalf("a refused add changed the Piglet directory: %v", left)
	}

	// Deleting the file the error names is the fix, and the inventory works afterwards.
	if err := os.Remove(origin); err != nil {
		t.Fatal(err)
	}
	copiesRequireOK(t, "add", filepath.Join(work, "left.yaml"))
	copiesRequireOK(t, "list", "--json")
	copiesRequireOK(t, "remove", "left", "--source")
}
