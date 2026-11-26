# Pre-flight

The cheap checks every open PR must pass before any heavy CI. One Go program
(`preflight`) runs them once an hour over every open PR of the repository, on
one runner, and records a commit status `preflight` and one comment on each PR.
What it checks is declared in `config.yaml`, `forbidden.yaml` and
`mandatory.yaml`; the program only runs it. The workflow is
`.github/workflows/preflight.yml`.

A PR that fails its checks is normal output and keeps the run green. A PR the
pre-flight could not deliver a verdict for is the pre-flight's own failure:
the `verify` job reports it in an issue and fails the run.

## Commands

| Command | Does |
|---|---|
| `preflight run` | fetch each PR's data, decide the cheap checks, run the scans, record status and comment |
| `preflight install` | put the pinned scanners of `config.yaml` into a bin directory |
| `preflight content` | apply `forbidden.yaml` and `mandatory.yaml` to a PR's changed files (a scan `config.yaml` runs) |
| `preflight verify` | check that every PR of a run got its status and comment, and keep the issue |
| `preflight checks` | list the checks of `config.yaml` |

A local dry run over real PRs, read only:

```sh
go build -o /tmp/preflight ./automation/ci/preflight
/tmp/preflight install --bin /tmp/pf-bin
GH_TOKEN=$(gh auth token) /tmp/preflight run --repo MichaelKinsy/PiG --root . \
  --bin /tmp/pf-bin --work /tmp/pf-work --prs "421 422" --dry-run --force-scans
```

## A run

1. List the open PRs (or take `--prs`), in number order, and load each PR's
   comment: it holds the state of the previous run.
2. Per PR, four at a time (`--jobs`): read the pull. Reuse the whole previous
   verdict when the pre-flight, the head, the base branch's tip and the
   description are unchanged and nothing was left undecided; that costs two
   API calls and writes nothing.
3. Otherwise fetch the PR's data (files, commits, compare with main, and the
   sources the relevant checks read), each source retried on its own: three
   retries after 2, 4 and 6 s. A source that still failed, or a file or commit
   list shorter than the PR's own count, is an error of every check that reads
   it; without the file list every path-gated check and every scan is an
   error too. An error fails the verdict and is decided again by the next run.
4. Decide the cheap checks from that data. Only when none failed, check out the
   head sparsely and run the scans in parallel.
5. Record: the status first (it is what holds the PR back), then the step
   summary, any alert, and the comment with the new state. A POST that
   creates a comment (an alert, a first comment) is sent once, and again only
   after a 429: after a 5xx or a broken connection GitHub may already have
   created it. The status POST is retried like a read (three retries after 2,
   4 and 6 s, after a 5xx, a broken connection, a timeout or a 429): GitHub
   keeps one status per head and context, so the same one sent twice changes
   nothing. Any other 4xx fails it at once.

A job fully checks at most `--budget` PRs (200). Reused PRs and PRs too large
for the API do not count. The PRs beyond go to next jobs of the same run
(`--next`), 200 each, at the same time, so every PR is judged every run. No PR
starts after `--time-budget` (25 minutes of the job's 30).

Measured on a GitHub runner (dry run over 40 of upstream's PRs, every scan):
under two minutes for all 40, a median of 7.6 s per PR with four at a time.
Starting a job takes about 50 s.

## config.yaml

`engine-revision`, `tools`, `checks` and `fix-title-exempt`.

`tools` are the scanners, pinned by release and SHA-256, or by a hash-locked
pip requirements file.

Each check:

| Key | Meaning |
|---|---|
| `when` | `always: true`, or `paths:` globs that make the check relevant (`**` crosses directories) |
| `stage` | `cheap`: decided from the PR's API data by a built-in rule; `scan`: an external tool on the PR's files |
| `reads` | (cheap) the data sources the rule reads: `pull`, `files`, `commits`, `compare-main`, `depends-on`, `dependency-graph`, `reviewed-ledger` (the reviewed ledger at the head commit) |
| `rule`, `with` | (cheap) the built-in rule of `rules.go` and its parameters |
| `scan` | (scan) the tool run, below |
| `main` | files in main's tree the verdict depends on (globs; a directory counts with everything in it) |
| `alert` | posted once as a comment of its own that mentions the PR's author when the check fails; again only after it passed in between, and never twice for the same head |

One cheap check also asks git. `all-commits` with `exempt-update-branch-merges: true` (the `signoff` check) lets one commit go without the message line: the merge of the base branch that GitHub's "Update branch" makes. The API data says that a commit has two parents and that GitHub committed it and verifies it; for such a commit the run fetches the PR's commits (the repository `git merge-tree` already uses when GitHub has not computed `mergeable`) and asks whether the second parent is on the base branch and whether the commit's tree is the one `git merge-tree --write-tree <first parent> <second parent>` writes. Only then is it exempt (`merges.go`); a merge that misses a condition fails with that condition named, and a git failure leaves the check an error, which the next run decides again.
| `revision` | raise it to decide the check again where nothing else changed |

A scan:

| Key | Meaning |
|---|---|
| `input` | `added`: the lines the PR adds, one file each, under `{input}` (a file GitHub shows no patch for is read whole from the head; line numbers are the head's). `changed`: the changed files at the head, under `{head}`, as `{files}`. `tree`: a repository holding only the changed files |
| `select` | a regular expression the changed files must match; none matching passes the check |
| `checkout` | more paths the scan reads at the head (`/*` is the whole tree) |
| `overlay` | files copied from main over the head before the run, so main's configuration runs, never the PR's |
| `run` | the command. `--` must stand before `{files}`. Placeholders: `{main}`, `{bin}`, `{self}` (this program), `{data}` (the PR's data as JSON), `{head}`, `{input}`, `{out}`, `{files}` |
| `dir`, `env` | working directory and extra environment |
| `exit` | the codes that mean clean and the ones that mean findings; any other is an error |
| `report` | where the findings are (`out` or `stdout`) and their format: `json`, `json-lines`, or `lines` read with a `pattern` whose named groups are `file`, `line` and `message`. A findings exit with no finding read is an error unless `unread-findings: pass` |
| `added-lines-only` | keep only findings on lines the PR adds |

### fix-title-exempt

The `bug-ledger` and `changelog` checks treat a commit titled as a fix (`Fix ...`, `fix: ...`, `<area>: fix ...`) as proof that the pull request fixes something, whatever its `Fix:` answer says. A feature pull request can carry such commits for code it adds itself: nothing released was faulty, so a bug-ledger entry and a `### Fixed` line would be untrue, and `Fix: no` fails. `fix-title-exempt` is the narrow way out:

```yaml
fix-title-exempt:
  - pr: 404
    reason: the fix-titled commits of this pull request repair code that the pull request itself adds; no released behaviour was faulty
    commits: [<full 40-digit sha>, ...]
```

With `Fix: no`, a fix-titled commit whose full sha is listed under this pull request's number is not evidence. The check passes with one `NOTE` line (and a notice annotation) that names the pull request, the commits and the reason. Everything else fails as before: a fix-titled commit the entry does not list (the message names the entry: its list must be reviewed again before it names the new commit), a listed sha in another pull request, a changed fragment that adds `### Fixed` (the exemption never covers fragments), and `Fix: yes` (it still needs its ledger change and its fragment with the fix marker). The entry is read from main's `config.yaml`, never from the pull request, so it takes effect only after its own pull request merges.

The configuration is an error for every pull request, never a silent skip, when an entry has no `pr`, a reason under 60 characters, no commits, a sha that is not 40 lowercase hex digits, a duplicate sha, an unknown key, or when two entries name the same pull request. Editing the list decides the `bug-ledger` and `changelog` checks again.

## Reuse and invalidation

A check's stored verdict is reused while its definition, its `main` files, its
`revision`, `engine-revision` and its inputs are unchanged. Editing one check
decides that check again and no other. Raise `engine-revision` when a change to
the engine's code changes what a stored verdict means. A new scanner, image or
Go version decides nothing again.

The base branch's tip is read once per run (`git/ref/heads/<ref>`): GitHub's
`base.sha` of a pull stays where the PR was last updated, so it cannot tell that
main moved.

## State

A PR's state is a JSON block in an HTML comment at the end of the pre-flight's
own comment on it: the verdicts with their input hashes, the head, the base
tip, a hash of the description, the status last set and the alerts posted. Only
a comment by the bot (`--bot`, `github-actions[bot]`) is read, so no one else
can hand a run a state; a state that does not parse is no state. The status is
set only when it or the head changed, the comment only when its text changed.
An alert comment ends with `<!-- pig-preflight-alert <check> <head> -->`. The
state is written after the alerts, so when that write fails the next run finds
the alert by its mark, in the bot's comments only, and does not post it again.

The state is unsigned: anyone with write access can edit the bot's comment.
The Actions cache is the intended place once upstream's cache has room.

A comment stays under GitHub's 65536 characters: warnings are folded to one per
check and file, at most 20 files per check, warning texts cut at 200, notices cut
at 600 when printed (the stored record holds them whole) and reasons at 1000
characters; when the comment is still too long, its warnings are left out.

## Content rules

`forbidden.yaml` lists what must not appear in the lines a PR adds: a pattern,
narrowed by `also` and by conditions on the value it captures (`not`,
`min-length`, `not-in`, `random`), or a built-in matcher where a pattern cannot
say it (`base64-blob`, `captured-time`, `home-path`, `prose`, `terms`; the last
three also over the fragments of a stream joined back together). `allow` lists
reviewed false positives, one file each, with the reason. The FG rules keep
provider captures out of the repository: fixtures are minimal synthetic shapes
made in the test.

`mandatory.yaml` lists what a changed file must contain. A pattern may name a
version of `variables/versions.yaml` as `{{ versions.<key> }}`, with the filters
`| minor` and `| url`; such a rule also runs over every file it matches when
`versions.yaml` changes. `listed-in` requires each matching changed file to be
named by a glob in another file (golden files in
`test/testdata-provenance.toml`).

`variables/versions.yaml` is the one source of the toolchain versions; the
copies elsewhere are `mandatory.yaml` rules.

Private terms (FG006) are words that must never appear in the repository but
cannot be published in `forbidden.yaml` either: names of people, customers or
internal hosts. They live in the repository secret `PIG_PRIVATE_TERMS`, one
term of at least three characters per line. The workflow writes the secret to
a file outside the checkout and names it in `PIG_PRIVATE_TERMS_FILE`; locally,
point that variable at a file of your own. A finding names the file, the line
and the term's line number, never the term. Without the secret the rule is off.

## Expected values changed with code

`test-expectation` fails a PR that changes an expected value in a `_test.go` file
together with production code of the same directory, unless the Pi behavior that
changed is cited (`Pi-Behavior-Change: <pi file.ts:line>` in the PR body and in a
commit, the file named by an inventory of main), or the reviewed ledger admits
the file. The ledger is `test/parity/testprune-reviewed.toml` read at the PR's
head commit; `make test-expectation-lint` admits by the same rows and validates
them with the same code (`reviewed/`). A row admits a changed test file when its
`path` is the file and its `commit` (at least ten characters) names exactly one
commit of the PR, and that commit changed the file (as the line lint requires;
the pre-flight asks the commit endpoint for the files of each such commit, one
call per commit and page of 100 files until a page is short, at most 30 pages;
a failure or a 404 there makes the check an error, and rows of more than 50
commits (`maxLedgerCommits`) are a failure that asks to split the PR, with no
call made; the answer is part of the check's input hash); a row of any other
commit or file admits nothing. A row of
class `CITE` needs a Pi file of main's inventories, `DIVERGENCE` a record in
`docs/parity/divergences/` (main's or the PR's), `NOBEHAVIOR` a reason. A
matching row that fails this, one whose commit does not change the file, an ambiguous commit prefix, and a ledger that is
not valid TOML are failures; a ledger the API cannot deliver makes the check an
error, and a head without the file admits nothing. Each admission prints a `NOTE`
line (and a `::notice`, and a row in the comment) naming the file, the row's
commit, class, cite and reason; a notice asks for no acknowledgement. The ledger
row is reviewed as part of the PR's diff.

Both gates leave the tooling directories out of this rule: a changed test file
under a `[[tooling]]` prefix of `test/testdata-provenance.toml` (PiG-owned CI
tooling with no Pi behavior to cite) is skipped, and needs no cite and no ledger
row (the line lint calls a row for such a file stale). The two gates read that
list through one package (`reviewed/`) and compare with a plain prefix match. The
pre-flight reads the list from main only, so a prefix that a PR adds exempts
nothing in that PR; a list that is missing, empty or not valid TOML on main
makes the check fail with that reason, never a silent skip. Each skip prints a
`NOTE` line (and a `::notice`, and a comment row) naming the file and the prefix.

## Acknowledgements

A warning (a copyright notice, licence text, an inline `gitleaks:allow`, or a
change to `.gitleaksignore` or `.gitleaks.toml`) does
not fail its check. `automation/ci/preflight/acknowledged.toml` at the PR's head
acknowledges a check's warnings for one file at one blob; a later change to the
file shows them again. An acknowledgement never covers a failing scan.

## Secrets

gitleaks runs from inside the added-lines tree (`dir .`) with main's
`.gitleaks.toml`, `--gitleaks-ignore-path {main}`, and `--ignore-gitleaks-allow`,
on the added lines only, so `^`-anchored `paths` in `.gitleaks.toml` and
relative fingerprints in main's `.gitleaksignore` match repository-relative
paths instead of a temporary runner directory. Only main's `.gitleaks.toml` and
`.gitleaksignore` govern the scan: if a PR adds or modifies `.gitleaksignore` or
`.gitleaks.toml`, pre-flight stages those files under neutral names in a
per-scan directory so gitleaks cannot load PR content as configuration (gitleaks
`dir` always loads `<source>/.gitleaksignore`), while still scanning their added
lines and mapping any findings back to the real path and head line. Any PR that
adds or changes `.gitleaksignore` or `.gitleaks.toml` also gets a
`gitleaks-config` warning row. Note that a `.gitleaksignore` fingerprint uses
the 1-based line number in the added-lines file (not the head file line number),
because gitleaks scans the extracted added-lines tree before pre-flight maps
findings back to PR head lines. A real false positive is allowed on main, by a
maintainer, in `.gitleaks.toml` (preferred over `.gitleaksignore`, whose
added-lines line numbers depend on each PR's patch). trufflehog reports only
secrets it verified, so a finding is a live credential. Either posts an alert
that mentions the author: revoke the secret and rewrite the branch.

## Trust

The program, its configuration and the scanners' configuration come from main,
never from the PR. A PR's symbolic links are replaced by files holding their
target before anything reads the head. The token reaches git through the
environment, as the header for the remote's host only. No program the
pre-flight starts (the scanners, git, pip) gets a variable whose name ends in
`_TOKEN`: not `GITHUB_TOKEN` or `GH_TOKEN`, nor the job's other credentials.
A PR's file names reach `git sparse-checkout` escaped, so each names one file
and none works as a pattern; a name with a line break fails the scans.

## verify

Every job writes what it did with each PR (`--handled`); the first job's report
lists every PR of the run and must be there. The `verify` job checks that each
judged PR has a `preflight` status on that head and the bot's comment with the
state for it. What is missing goes into one issue, found by its marker through
the search API: opened, updated when the list changes, closed once all is
well, reopened rather than made anew. A head GitHub no longer knows (a
force-push) is skipped: its new head is judged next run.

## Tests

```sh
go test ./automation/ci/preflight/
go tool golangci-lint run ./automation/ci/preflight/
go tool deadcode -test ./automation/ci/preflight/
go fix -diff ./automation/ci/preflight/
```

Mutation testing with gremlins runs in a copy of the package as its own module,
under a memory cap: a mutant can turn a loop into one that grows without end.

```sh
systemd-run --user --scope -p MemoryMax=6G -p MemorySwapMax=0 gremlins unleash
```

## On a fork

A branch `ci/preflight` holds this code with every other workflow removed, and
a push to `ci/run` (that branch and an empty commit) runs the pre-flight for
real over the fork's PRs against `ci/preflight` only, so nothing else starts and
the fork's own PRs are never judged.
