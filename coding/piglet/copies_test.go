package piglet

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
		"backslash":          {`prompts\system.md`: digest},
		"drive":              {"C:/prompt.md": digest},
		"parent":             {"../prompt.md": digest},
		"dot prefix":         {"./prompt.md": digest},
		"absolute":           {"/etc/passwd": digest},
		"home":               {"~/prompt.md": digest},
		"git metadata":       {".git/config": digest},
		"digest not sha256":  {"prompt.md": "md5:00"},
		"uppercase digest":   {"prompt.md": "sha256:" + strings.ToUpper(strings.TrimPrefix(digest, "sha256:"))},
		"missing digest":     {"prompt.md": ""},
		"directory itself":   {".": digest},
		"trailing separator": {"prompts/": digest},
	} {
		if err := (pigletCopies{Files: files}).validate(); err == nil {
			t.Errorf("%s: record with %v validated", name, files)
		}
	}
	if err := (pigletCopies{Files: map[string]string{"prompts/system.md": digest}}).validate(); err != nil {
		t.Fatalf("portable record rejected: %v", err)
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

func TestClaimKeyFoldsCaseWhereTheFileSystemDoes(t *testing.T) {
	folds := runtime.GOOS == "windows" || runtime.GOOS == "darwin"
	if got := claimKey(filepath.Join("a", "Prompt.md")) == claimKey(filepath.Join("a", "prompt.MD")); got != folds {
		t.Fatalf("case-only difference compares equal = %v on %s, want %v", got, runtime.GOOS, folds)
	}
	if claimKey(filepath.Join("a", "x", "..", "prompt.md")) != claimKey(filepath.Join("a", "prompt.md")) {
		t.Fatal("claim keys do not clean paths")
	}
}
