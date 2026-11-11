package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MichaelKinsy/PiG/internal/codingagent"
	"github.com/MichaelKinsy/PiG/internal/testenv"
)

func TestStatusJSONReportsHealthyEmptyState(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Setenv("HOME", root)
	t.Chdir(cwd)
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	assertNoTopLevelVersionJSON(t, stdout)
	if !status.Healthy || status.Packages.Total != 0 || status.Resources.Total != 0 || status.Piglets.Total != 0 || len(status.Errors) != 0 {
		t.Fatalf("status = %#v", status)
	}
	if status.Paths.Home != canonicalStatusPath(root) || status.Paths.Agent != canonicalStatusPath(agentDir) || status.Paths.Project != canonicalStatusPath(filepath.Join(cwd, ".pig")) {
		t.Fatalf("paths = %#v", status.Paths)
	}
}

func TestStatusFailsOnDuplicateResourceIdentity(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	for _, directory := range []string{"one", "two"} {
		writeResourceFixture(t, filepath.Join(agentDir, "skills", directory, "SKILL.md"), "---\nname: duplicate\n---\n")
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
	if code != 1 || stderr != "" || !strings.Contains(stdout, "duplicate skills resource") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
}

func TestStatusJSONFailsOnUnmaterializedPackage(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: filepath.Join(root, "missing")}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json"})
	})
	if code != 1 || stderr != "" {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if status.Healthy || len(status.Errors) == 0 || !strings.Contains(strings.Join(status.Errors, "\n"), "not materialized") {
		t.Fatalf("status = %#v", status)
	}
}

// A configured Package source may carry credentials (a git URL with a user and
// token, or a secret query parameter). `pig status` is an aggregate report meant
// to be pasted or piped to other tools, and the messages of the layers below
// name the source they failed on, so it names a URL as Pi's bug report does
// (redactUrl): without the credentials.
func TestStatusRedactsCredentialsInPackageSources(t *testing.T) {
	const gitSource = "git:https://deploy:S3CRET-PASSWORD@example.invalid/org/repo.git"
	const querySource = "git:https://example.invalid/org/other.git?access_token=S3CRET-QUERY"
	for name, test := range map[string]struct {
		sources   []string
		install   bool
		wantCode  int
		wantNamed string
	}{
		"installed Package resources carry the source":  {[]string{gitSource}, true, 0, "example.invalid/org/repo.git"},
		"unmaterialized Package error names the source": {[]string{gitSource, querySource}, false, 1, "example.invalid/org/other.git"},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			agentDir := filepath.Join(root, "agent")
			cwd := filepath.Join(root, "workspace")
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PIG_HOME", root)
			t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
			t.Chdir(cwd)
			var sources []codingagent.PackageSource
			for _, source := range test.sources {
				sources = append(sources, codingagent.PackageSource{Source: source})
			}
			if err := codingagent.NewSettingsManager(cwd, agentDir).SetPackages(sources); err != nil {
				t.Fatal(err)
			}
			if test.install {
				writeStatusSkillPackage(t, filepath.Join(agentDir, "git", "example.invalid", "org", "repo"))
			}

			stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
			if code != test.wantCode || stderr != "" {
				t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			text, textErr, _ := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status"}) })
			for _, output := range []string{stdout, text + textErr} {
				for _, secret := range []string{"S3CRET-PASSWORD", "S3CRET-QUERY", "deploy:"} {
					if strings.Contains(output, secret) {
						t.Errorf("status carries %q:\n%s", secret, output)
					}
				}
			}
			if !strings.Contains(stdout, test.wantNamed) {
				t.Errorf("status no longer names the Package %q:\n%s", test.wantNamed, stdout)
			}
		})
	}
}

// The redacted form is Pi's redactUrl, byte for byte: user information dropped, a sensitive query value replaced by `<redacted>` and form-encoded, the rest
// of the URL as written. Status prints it in the JSON report, the text report and its errors.
func TestStatusPrintsRedactedPackageSourcesAsPisRedactUrlSpellsThem(t *testing.T) {
	const gitSource = "git:https://deploy:S3CRET-PASSWORD@example.invalid/org/repo.git"
	const querySource = "git:https://example.invalid/org/other.git?access_token=S3CRET-QUERY&ref=main"
	const wantGit = "git:https://example.invalid/org/repo.git"
	const wantQuery = "git:https://example.invalid/org/other.git?access_token=%3Credacted%3E&ref=main"
	setup := func(t *testing.T, sources ...string) {
		root := t.TempDir()
		agentDir := filepath.Join(root, "agent")
		cwd := filepath.Join(root, "workspace")
		if err := os.MkdirAll(cwd, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PIG_HOME", root)
		t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
		t.Chdir(cwd)
		var packages []codingagent.PackageSource
		for _, source := range sources {
			packages = append(packages, codingagent.PackageSource{Source: source})
		}
		if err := codingagent.NewSettingsManager(cwd, agentDir).SetPackages(packages); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("an installed Package's resources carry the source", func(t *testing.T) {
		setup(t, gitSource)
		writeStatusSkillPackage(t, filepath.Join(os.Getenv("PIG_CODING_AGENT_DIR"), "git", "example.invalid", "org", "repo"))
		stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
		if code != 0 || stderr != "" {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		var report statusOutput
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatal(err)
		}
		if len(report.Resources.Items) == 0 {
			t.Fatalf("no resources in %s", stdout)
		}
		for _, item := range report.Resources.Items {
			if item.Source != wantGit {
				t.Errorf("resource source = %q, want %q", item.Source, wantGit)
			}
		}
	})
	t.Run("an error names the source without credentials", func(t *testing.T) {
		setup(t, gitSource, querySource)
		stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
		if code != 1 || stderr != "" {
			t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
		}
		var report statusOutput
		if err := json.Unmarshal([]byte(stdout), &report); err != nil {
			t.Fatal(err)
		}
		errors := strings.Join(report.Errors, "\n")
		for _, want := range []string{wantGit, wantQuery} {
			if !strings.Contains(errors, want) {
				t.Errorf("errors = %q, want one naming %q", report.Errors, want)
			}
		}
		text, textErr, _ := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status"}) })
		for _, want := range []string{wantGit, wantQuery} {
			if !strings.Contains(text+textErr, want) {
				t.Errorf("text report does not name %q:\n%s%s", want, text, textErr)
			}
		}
	})
}

// A password may hold any character a URL's user information allows, a quote
// included; the redaction of a message must not stop at it and print the rest.
func TestStatusRedactsAPasswordThatHoldsAQuote(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	const source = "git:https://deploy:S3CRET'TAILPART@example.invalid/org/repo.git"
	if err := codingagent.NewSettingsManager(cwd, agentDir).SetPackages([]codingagent.PackageSource{{Source: source}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, _ := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
	text, textErr, _ := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status"}) })
	for _, output := range []string{stdout, text + textErr} {
		for _, secret := range []string{"S3CRET", "TAILPART"} {
			if strings.Contains(output, secret) {
				t.Errorf("status carries %q:\n%s", secret, output)
			}
		}
		if !strings.Contains(output, "example.invalid/org/repo.git") {
			t.Errorf("status no longer names the Package:\n%s", output)
		}
	}
}

// A settings file with a syntax error reports it as Node's JSON.parse does,
// quoting about twenty characters around the error. In a file that holds a
// credential the quote may be cut out of the middle of it, where no URL is left
// to redact, so `pig status` (which has no Pi output to match) reports the
// error without the quoted text.
func TestStatusSettingsSyntaxErrorQuotesNoFileText(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "workspace")
	for _, directory := range []string{cwd, agentDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PIG_HOME", root)
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"packages": ["git:https://example.invalid/r.git", @SECRETPASS1234 ]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--json"}) })
	if code != 1 || stderr != "" || !strings.Contains(stdout, "settings: Unexpected token '@'") || !strings.Contains(stdout, "is not valid JSON") {
		t.Fatalf("code=%d stdout=%s stderr=%s: want the settings syntax error", code, stdout, stderr)
	}
	if strings.Contains(stdout, "SECRETPAS") {
		t.Errorf("the settings error quotes the file:\n%s", stdout)
	}
}

func TestStatusHelpAndUnknownOption(t *testing.T) {
	stdout, stderr, code := captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--help"}) })
	if code != 0 || stderr != "" || !strings.Contains(stdout, "Usage:") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	_, stderr, code = captureStdoutStderr(t, func() int { return runStatusCommand([]string{"status", "--unknown"}) })
	if code != 2 || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
}

func TestStatusAtHomeDoesNotReportGlobalRootAsProject(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, ".pig", "agent")
	t.Setenv("HOME", home)
	t.Setenv("PIG_HOME", filepath.Join(home, ".pig"))
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(home)

	status := collectStatus()
	if status.Paths.Project != "" {
		t.Fatalf("global root reported as project path: %#v", status.Paths)
	}
}

func TestStatusAcceptsDisabledMissingPackageMember(t *testing.T) {
	cwd, agentDir, packageRoot := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	writeResourceFixture(t, filepath.Join(packageRoot, "package.json"), `{"name":"pkg","pi":{"extensions":["extensions/removed"]}}`)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: packageRoot, Extensions: []string{"-extensions/removed"}}}); err != nil {
		t.Fatal(err)
	}

	status := collectStatus()
	if !status.Healthy || len(status.Errors) != 0 {
		t.Fatalf("disabled missing member made status unhealthy: %#v", status)
	}
	var missing *statusResourceItem
	for i := range status.Resources.Items {
		if status.Resources.Items[i].Name == "removed" {
			missing = &status.Resources.Items[i]
		}
	}
	if missing == nil || missing.Health != "missing" || missing.Enabled {
		t.Fatalf("missing resource item = %#v in %#v", missing, status.Resources.Items)
	}
}

func writeStatusSkillPackage(t *testing.T, root string) {
	t.Helper()
	writeResourceFixture(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"skills":["skills"]}}`)
	for _, name := range []string{"alpha", "beta"} {
		writeResourceFixture(t, filepath.Join(root, "skills", name, "SKILL.md"), "---\nname: "+name+"\ndescription: d\n---\n")
	}
}

func TestStatusIgnoresFilterDisabledDuplicatePackageResources(t *testing.T) {
	cwd, agentDir, first, second := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	writeStatusSkillPackage(t, first)
	writeStatusSkillPackage(t, second)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: first}, {Source: second, Skills: []string{}}}); err != nil {
		t.Fatal(err)
	}
	status := collectStatus()
	if !status.Healthy || len(status.Errors) != 0 {
		t.Fatalf("disabled copies reported as duplicates: %#v", status.Errors)
	}
}

func TestStatusStillFailsOnEnabledDuplicatePackageResources(t *testing.T) {
	cwd, agentDir, first, second := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	writeStatusSkillPackage(t, first)
	writeStatusSkillPackage(t, second)
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: first}, {Source: second}}); err != nil {
		t.Fatal(err)
	}
	status := collectStatus()
	if status.Healthy || !strings.Contains(strings.Join(status.Errors, "\n"), "duplicate skills resource") {
		t.Fatalf("enabled duplicates not reported: %#v", status)
	}
}

func writeResourceFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeStatusExtensionPackage writes a Package whose only extension is
// src/index.ts, the layout pi-fff, pi-subagents and pi-subdir-context share.
func writeStatusExtensionPackage(t *testing.T, root, name string) {
	t.Helper()
	writeResourceFixture(t, filepath.Join(root, "package.json"), `{"name":"`+name+`","pi":{"extensions":["./src/index.ts"]}}`)
	writeResourceFixture(t, filepath.Join(root, "src", "index.ts"), "export default function (pi) {}\n")
}

// TestStatusWarnsOnDuplicatePackageExtensionIdentity is the regression for
// MichaelKinsy/PiG#299. Two Packages that both expose src/index.ts resolve to
// the same "src" public name. The loader keys an extension by path and
// DisambiguateIdentities renames the later copies ("src:2"), so both load and
// status must stay healthy; the shared name is reported as a warning.
func TestStatusWarnsOnDuplicatePackageExtensionIdentity(t *testing.T) {
	cwd, agentDir, first, second := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	writeStatusExtensionPackage(t, first, "first")
	writeStatusExtensionPackage(t, second, "second")
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: first}, {Source: second}}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json", "--no-input"})
	})
	if code != 0 || stderr != "" {
		t.Fatalf("code=%d stderr=%s", code, stderr)
	}
	var status statusOutput
	if err := json.Unmarshal([]byte(stdout), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Healthy || len(status.Errors) != 0 {
		t.Fatalf("duplicate extension name reported as fatal: %#v", status.Errors)
	}
	joined := strings.Join(status.Warnings, "\n")
	if !strings.Contains(joined, `duplicate extensions resource "src"`) {
		t.Fatalf("warnings = %#v, want the duplicate extension name reported", status.Warnings)
	}
}

// TestStatusStillFailsOnDuplicateExtensionPublicNameGuard keeps the fatal path
// honest: a kind that keys its runtime set by name stays an error. Skills do,
// so two Packages exposing the same skill name remain a failure.
func TestStatusStillFailsOnDuplicateNonExtensionResourceIdentity(t *testing.T) {
	cwd, agentDir, first, second := t.TempDir(), t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PIG_HOME", t.TempDir())
	t.Setenv("PIG_CODING_AGENT_DIR", agentDir)
	t.Chdir(cwd)
	for _, root := range []string{first, second} {
		writeResourceFixture(t, filepath.Join(root, "package.json"), `{"name":"pkg","pi":{"skills":["skills"]}}`)
		writeResourceFixture(t, filepath.Join(root, "skills", "dup", "SKILL.md"), "---\nname: dup\ndescription: d\n---\n")
	}
	settings := codingagent.NewSettingsManager(cwd, agentDir)
	if err := settings.SetPackages([]codingagent.PackageSource{{Source: first}, {Source: second}}); err != nil {
		t.Fatal(err)
	}
	stdout, _, code := captureStdoutStderr(t, func() int {
		return runStatusCommand([]string{"status", "--json", "--no-input"})
	})
	if code != 1 || !strings.Contains(stdout, "duplicate skills resource") {
		t.Fatalf("code=%d stdout=%s", code, stdout)
	}
}

// A path through a symbolic link names the same directory whether or not the part below the link exists yet, so the report does not
// change when a run creates the agent directory (on macOS the temporary directory is a link: /var -> /private/var).
func TestCanonicalStatusPathResolvesTheExistingPartOfAMissingPath(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	link := filepath.Join(root, "link")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	testenv.RequireDirectoryLink(t, real, link)
	canonicalReal := canonicalStatusPath(real)
	missing := filepath.Join(link, "agent", ".pig")
	if got, want := canonicalStatusPath(missing), filepath.Join(canonicalReal, "agent", ".pig"); got != want {
		t.Errorf("missing path = %q, want %q", got, want)
	}
	if err := os.MkdirAll(filepath.Join(real, "agent", ".pig"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, want := canonicalStatusPath(missing), filepath.Join(canonicalReal, "agent", ".pig"); got != want {
		t.Errorf("created path = %q, want %q", got, want)
	}
}
