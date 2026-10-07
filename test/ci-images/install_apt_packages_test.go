package ciimages

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type aptFixture struct {
	bin, sources, apt, curl, timeout string
	list, mirrors                    string
	dir                              string
	env                              []string
}

// newAptFixture builds the runner's apt layout and fake commands. curl exits curlStatus; apt-get exits 1 for its first aptFailures update calls.
// timeout logs its arguments and runs the command without a deadline, so a test reads the deadlines the script chose.
func newAptFixture(t *testing.T, curlStatus string, aptFailures int) aptFixture {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("the apt installer runs on Ubuntu runners")
	}
	dir := t.TempDir()
	f := aptFixture{
		dir: dir, bin: filepath.Join(dir, "bin"), sources: filepath.Join(dir, "sources.list.d"),
		apt: filepath.Join(dir, "apt.log"), curl: filepath.Join(dir, "curl.log"), timeout: filepath.Join(dir, "timeout.log"), list: filepath.Join(dir, "sources.list"),
		mirrors: filepath.Join(dir, "apt-mirrors.txt"),
	}
	for _, d := range []string{f.bin, f.sources} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string, mode os.FileMode) {
		if err := os.WriteFile(path, []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.sources, "ubuntu.sources"), "Types: deb\nURIs: http://azure.archive.ubuntu.com/ubuntu/\nSuites: noble\n", 0o600)
	write(filepath.Join(f.sources, "microsoft-prod.list"), "deb https://packages.microsoft.com/ubuntu/24.04/prod noble main\n", 0o600)
	write(f.list, "", 0o600)
	write(filepath.Join(f.bin, "curl"), "#!/bin/sh\necho \"$@\" >> '"+f.curl+"'\nexit "+curlStatus+"\n", 0o700)
	write(filepath.Join(f.bin, "sleep"), "#!/bin/sh\nexit 0\n", 0o700)
	write(filepath.Join(f.bin, "timeout"), "#!/bin/sh\necho \"$1 $2 $3\" >> '"+f.timeout+"'\nshift 2\nexec \"$@\"\n", 0o700)
	write(filepath.Join(f.bin, "apt-get"), `#!/bin/sh
echo "$*" >> '`+f.apt+`'
case " $* " in
*" update "*)
	n=$(cat '`+f.dir+`/updates' 2>/dev/null || echo 0)
	n=$((n + 1))
	echo $n > '`+f.dir+`/updates'
	[ "$n" -le `+itoa(aptFailures)+` ] && exit 1
	[ -n "$APT_FAIL_WHILE_AZURE" ] && grep -rqs azure.archive.ubuntu.com "$APT_SOURCES_DIR" '`+f.mirrors+`' && exit 1
	;;
*" install "*)
	[ -n "$APT_FAIL_INSTALL_WHILE_AZURE" ] && grep -rqs azure.archive.ubuntu.com "$APT_SOURCES_DIR" '`+f.mirrors+`' && exit 100
esac
exit 0
`, 0o700)
	return f
}

// useMirrorList switches the fixture to the runner's real layout: the sources name no URL, only a mirror list that names Azure.
func (f aptFixture) useMirrorList(t *testing.T) {
	t.Helper()
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(f.sources, "ubuntu.sources"), "Types: deb\nURIs: mirror+file:"+f.mirrors+"\nSuites: noble\n")
	write(f.mirrors, "http://azure.archive.ubuntu.com/ubuntu/\tpriority:1\nhttp://security.ubuntu.com/ubuntu/\tpriority:2\n")
}

func itoa(n int) string { return strconv.Itoa(n) }

func (f aptFixture) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(filepath.Join(repoRoot(t), "automation", "ci", "install-apt-packages.sh"), args...)
	cmd.Env = append(os.Environ(),
		"PATH="+f.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"SUDO=", "APT_SOURCES_DIR="+f.sources, "APT_SOURCES_LIST="+f.list)
	cmd.Env = append(cmd.Env, f.env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// withEnv adds environment variables to the script run.
func (f aptFixture) withEnv(env ...string) aptFixture {
	f.env = append(f.env[:len(f.env):len(f.env)], env...)
	return f
}

func (f aptFixture) read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// An unreachable Azure mirror moves apt to the public archive, so an outage of one mirror does not fail the install.
func TestInstallAptPackagesFallsBackWhenAzureMirrorIsUnreachable(t *testing.T) {
	f := newAptFixture(t, "28", 0)
	if out, err := f.run(t, "tmux", "ripgrep"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	sources := f.read(t, filepath.Join(f.sources, "ubuntu.sources"))
	if strings.Contains(sources, "azure.archive.ubuntu.com") || !strings.Contains(sources, "http://archive.ubuntu.com/ubuntu/") {
		t.Fatalf("sources were not moved to the public archive:\n%s", sources)
	}
	if !strings.Contains(f.read(t, f.apt), "install --yes tmux ripgrep") {
		t.Fatalf("packages not installed:\n%s", f.read(t, f.apt))
	}
	if _, err := os.Stat(filepath.Join(f.sources, "microsoft-prod.list")); err == nil {
		t.Fatal("the Microsoft source was kept")
	}
}

// A reachable Azure mirror stays in use: it is the fast mirror inside the runner's network.
func TestInstallAptPackagesKeepsAzureMirrorWhenReachable(t *testing.T) {
	f := newAptFixture(t, "0", 0)
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(f.read(t, filepath.Join(f.sources, "ubuntu.sources")), "azure.archive.ubuntu.com") {
		t.Fatal("a reachable Azure mirror was replaced")
	}
}

// A failed update retries the update-and-install pair, and a persistent failure fails the step after three attempts.
func TestInstallAptPackagesRetriesAndFailsClosed(t *testing.T) {
	f := newAptFixture(t, "0", 2)
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("two transient failures were not retried: %v\n%s", err, out)
	}
	if n := strings.Count(f.read(t, f.apt), " update"); n != 3 {
		t.Fatalf("update ran %d times, want 3", n)
	}
	g := newAptFixture(t, "0", 99)
	out, err := g.run(t, "tmux")
	if err == nil {
		t.Fatalf("persistent failure was reported as success:\n%s", out)
	}
	if n := strings.Count(g.read(t, g.apt), " update"); n != 3 {
		t.Fatalf("update ran %d times, want 3", n)
	}
	if strings.Contains(g.read(t, g.apt), "install") {
		t.Fatal("install ran after a failed update")
	}
}

func TestInstallAptPackagesRequiresPackages(t *testing.T) {
	f := newAptFixture(t, "0", 0)
	if _, err := f.run(t); err == nil {
		t.Fatal("no packages was accepted")
	}
}

// An update that cannot fetch every index on a mirror whose release file answers moves apt to the public archive
// and retries, instead of repeating the failing mirror. apt-get update must run with --error-on=any so a partial
// index failure counts as a failure.
func TestInstallAptPackagesSwitchesMirrorWhenUpdateFails(t *testing.T) {
	f := newAptFixture(t, "0", 0).withEnv("APT_FAIL_WHILE_AZURE=1")
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("install failed after the mirror switch: %v\n%s", err, out)
	}
	sources := f.read(t, filepath.Join(f.sources, "ubuntu.sources"))
	if strings.Contains(sources, "azure.archive.ubuntu.com") || !strings.Contains(sources, "http://archive.ubuntu.com/ubuntu/") {
		t.Fatalf("sources were not moved to the public archive after the update failed:\n%s", sources)
	}
	log := f.read(t, f.apt)
	if n := strings.Count(log, "--error-on=any update"); n != 2 {
		t.Fatalf("update with --error-on=any ran %d times, want 2:\n%s", n, log)
	}
	if !strings.Contains(log, "install --yes tmux") {
		t.Fatalf("packages not installed:\n%s", log)
	}
}

// A mirror that serves the indexes but fails the package downloads moves apt to the public archive: the install fails,
// the sources change once, and the retry updates and installs against the public archive.
func TestInstallAptPackagesSwitchesMirrorWhenDownloadFails(t *testing.T) {
	for _, mirrorList := range []bool{false, true} {
		f := newAptFixture(t, "0", 0).withEnv("APT_FAIL_INSTALL_WHILE_AZURE=1")
		if mirrorList {
			f.useMirrorList(t)
		}
		if out, err := f.run(t, "tmux"); err != nil {
			t.Fatalf("mirrorList=%v: install failed after the mirror switch: %v\n%s", mirrorList, err, out)
		}
		if strings.Contains(f.read(t, filepath.Join(f.sources, "ubuntu.sources"))+f.read(t, f.mirrors), "azure.archive.ubuntu.com") {
			t.Fatalf("mirrorList=%v: Azure was kept after the download failed", mirrorList)
		}
		log := f.read(t, f.apt)
		if n := strings.Count(log, "--error-on=any update"); n != 2 {
			t.Fatalf("mirrorList=%v: update ran %d times, want 2:\n%s", mirrorList, n, log)
		}
		if n := strings.Count(log, "install --yes tmux"); n != 2 {
			t.Fatalf("mirrorList=%v: install ran %d times, want 2:\n%s", mirrorList, n, log)
		}
	}
}

// A download failure on the public archive does not return to Azure or switch again: the script retries there and fails closed.
func TestInstallAptPackagesDoesNotLoopBetweenMirrorsOnDownloadFailure(t *testing.T) {
	f := newAptFixture(t, "28", 0)
	apt := "#!/bin/sh\necho \"$*\" >> '" + f.apt + "'\ncase \" $* \" in *\" install \"*) exit 100;; esac\nexit 0\n"
	if err := os.WriteFile(filepath.Join(f.bin, "apt-get"), []byte(apt), 0o700); err != nil {
		t.Fatal(err)
	}
	out, err := f.run(t, "tmux")
	if err == nil {
		t.Fatalf("a persistent download failure was reported as success:\n%s", out)
	}
	if n := strings.Count(out, "using archive.ubuntu.com"); n != 1 {
		t.Fatalf("the mirror switched %d times, want 1:\n%s", n, out)
	}
	if n := strings.Count(f.read(t, f.apt), "install --yes tmux"); n != 3 {
		t.Fatalf("install ran %d times, want 3", n)
	}
	if strings.Contains(f.read(t, filepath.Join(f.sources, "ubuntu.sources")), "azure.archive.ubuntu.com") {
		t.Fatal("the sources returned to Azure")
	}
}

// Without an Azure source there is nothing to switch, so a failing update retries and then fails closed.
func TestInstallAptPackagesLeavesOtherSourcesAlone(t *testing.T) {
	f := newAptFixture(t, "0", 99)
	if err := os.WriteFile(filepath.Join(f.sources, "ubuntu.sources"), []byte("URIs: http://mirror.example/ubuntu/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.run(t, "tmux"); err == nil {
		t.Fatal("persistent failure was reported as success")
	}
	if got := f.read(t, filepath.Join(f.sources, "ubuntu.sources")); got != "URIs: http://mirror.example/ubuntu/\n" {
		t.Fatalf("an unrelated source changed:\n%s", got)
	}
}

// Every apt-get call runs under timeout --kill-after=10s, and the deadlines plus retries fit the 10-minute workflow step limit.
func TestInstallAptPackagesDeadlinesFitTheStepLimit(t *testing.T) {
	f := newAptFixture(t, "0", 99)
	if _, err := f.run(t, "tmux"); err == nil {
		t.Fatal("persistent failure was reported as success")
	}
	lines := strings.Split(strings.TrimSpace(f.read(t, f.timeout)), "\n")
	if len(lines) != 3 {
		t.Fatalf("timeout ran %d times, want 3:\n%v", len(lines), lines)
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "--kill-after=10s 90s apt-get") {
			t.Fatalf("update deadline is %q, want --kill-after=10s 90s", line)
		}
	}
	g := newAptFixture(t, "0", 0)
	if _, err := g.run(t, "tmux"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(g.read(t, g.timeout)), "\n"); len(got) != 2 || !strings.HasPrefix(got[1], "--kill-after=10s 150s apt-get") {
		t.Fatalf("install deadline is %v, want --kill-after=10s 150s", got)
	}
	// Deadlines alone could run 3 x (90 + 150 + 2 x 10) + 30 = 810s, so only the script's budget keeps it inside the step limit.
	const budget, grace, stepLimit = 480, 10, 600
	if budget+grace >= stepLimit {
		t.Fatalf("budget %ds does not leave room inside the %ds step limit", budget, stepLimit)
	}
}

// The remaining budget caps each deadline, so retries cannot outlive the workflow step.
func TestInstallAptPackagesBudgetCapsDeadlines(t *testing.T) {
	f := newAptFixture(t, "0", 99).withEnv("APT_BUDGET_SECONDS=60")
	if _, err := f.run(t, "tmux"); err == nil {
		t.Fatal("persistent failure was reported as success")
	}
	if first, _, _ := strings.Cut(f.read(t, f.timeout), "\n"); !strings.HasPrefix(first, "--kill-after=10s 50s apt-get") {
		t.Fatalf("first deadline %q was not capped to the 60s budget minus the kill grace", first)
	}
	g := newAptFixture(t, "0", 99).withEnv("APT_BUDGET_SECONDS=5")
	out, err := g.run(t, "tmux")
	if err == nil || !strings.Contains(out, "budget of 5s is spent") {
		t.Fatalf("a spent budget did not fail closed: %v\n%s", err, out)
	}
	if g.read(t, g.apt) != "" {
		t.Fatalf("apt-get ran after the budget was spent:\n%s", g.read(t, g.apt))
	}
}

// The runner's sources reference a mirror list rather than the Azure URL. An unreachable Azure mirror must rewrite the list,
// because apt reads its URIs from there; sources that name no Azure URL give a rewrite of the sources nothing to replace.
func TestInstallAptPackagesRewritesMirrorListWhenAzureMirrorIsUnreachable(t *testing.T) {
	f := newAptFixture(t, "28", 0)
	f.useMirrorList(t)
	sourcesBefore := f.read(t, filepath.Join(f.sources, "ubuntu.sources"))
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	mirrors := f.read(t, f.mirrors)
	if strings.Contains(mirrors, "azure.archive.ubuntu.com") || !strings.Contains(mirrors, "http://archive.ubuntu.com/ubuntu/\tpriority:1") {
		t.Fatalf("the mirror list was not moved to the public archive:\n%s", mirrors)
	}
	if !strings.Contains(mirrors, "http://security.ubuntu.com/ubuntu/\tpriority:2") {
		t.Fatalf("an unrelated mirror changed:\n%s", mirrors)
	}
	if got := f.read(t, filepath.Join(f.sources, "ubuntu.sources")); got != sourcesBefore {
		t.Fatalf("the sources changed:\n%s", got)
	}
}

// A mirror list whose Azure host answers the release probe but fails the index fetch moves to the public archive and retries.
func TestInstallAptPackagesSwitchesMirrorListWhenUpdateFails(t *testing.T) {
	f := newAptFixture(t, "0", 0).withEnv("APT_FAIL_WHILE_AZURE=1")
	f.useMirrorList(t)
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("install failed after the mirror switch: %v\n%s", err, out)
	}
	if mirrors := f.read(t, f.mirrors); strings.Contains(mirrors, "azure.archive.ubuntu.com") {
		t.Fatalf("the mirror list kept Azure:\n%s", mirrors)
	}
	if n := strings.Count(f.read(t, f.apt), "--error-on=any update"); n != 2 {
		t.Fatalf("update ran %d times, want 2", n)
	}
}

// A reachable Azure mirror stays in the mirror list.
func TestInstallAptPackagesKeepsMirrorListWhenAzureIsReachable(t *testing.T) {
	f := newAptFixture(t, "0", 0)
	f.useMirrorList(t)
	before := f.read(t, f.mirrors)
	if out, err := f.run(t, "tmux"); err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if got := f.read(t, f.mirrors); got != before {
		t.Fatalf("a reachable Azure mirror was replaced:\n%s", got)
	}
}
