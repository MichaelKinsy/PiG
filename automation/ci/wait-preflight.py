#!/usr/bin/env python3
# SPDX-License-Identifier: MIT
"""Wait for the pre-flight verdict of a pull request head and exit with it.

Usage: wait-preflight.py <owner/repo> <head-sha> <default-branch>
Environment: GH_TOKEN (scopes: statuses read, actions read); GITHUB_API_URL (default https://api.github.com);
GITHUB_STEP_SUMMARY (optional).

.github/workflows/preflight.yml (the program in automation/ci/preflight) judges a pull request when it opens or changes and
records the "preflight" commit status on the head. The status is set only when a PR's evaluation ends: no status means the
run has not finished that PR, and "pending" is a result ("scans still to run", "waits for the next run"), not "running". So
the wait follows the STATE of the pre-flight workflow run for this head, never a clock:

  success                        exit 0
  failure, error                 exit 1 with the status's description (the pre-flight's reason)
  no verdict, a run queued or    keep waiting (a slow runner pool is not the pull request's fault)
  in progress for this head
  no verdict, every run for      exit 1 at once, naming the run and its conclusion (cancelled, skipped,
  this head completed            startup failure, a failed job): no run is left to set a verdict
  no run for this head           exit 1 after SETTLING_READS reads of the run list, SETTLING_SECONDS apart (10 minutes:
                                 on a slow Actions the run itself can be created late)

The only clock left is the job's timeout-minutes in ci.yml. The runs are listed before the status is read, so a status
recorded between the two reads is seen by this poll or the next, never lost to a run that ended in between. A pull_request_target run
carries the pull request's head as its head_sha (a schedule or dispatch run carries main's), so the head and the event select
exactly the runs of this head; an `edited` event starts another run on the same head and the concurrency group cancels the older one, so
any active run keeps the wait.

The wait is skipped in one case only: the DEFAULT branch has no .github/workflows/preflight.yml. GitHub runs
pull_request_target from the default branch whatever the pull request's base, so without the file no workflow could ever
record a status. The default branch is read through the API, never the head tree, so a pull request cannot skip its own
gate by deleting the file. Any other answer of the API, an error included, keeps the wait.

An API fault outside our control (a 5xx, a 429 or a rate-limit 403, a network error) is retried: FAULT_ATTEMPTS tries in
all, FAULT_BACKOFF seconds apart. This is the one retry here; every other answer is acted on at once.
"""

import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass, field

WORKFLOW = ".github/workflows/preflight.yml"
CONTEXT = "preflight"
EVENT = "pull_request_target"

# One call is tried FAULT_ATTEMPTS times (the first plus three retries), waiting FAULT_BACKOFF[i] seconds after try i.
# The bound is 4 tries and 35 s of waiting per call; the fault is then reported and the job fails.
FAULT_ATTEMPTS = 4
FAULT_BACKOFF = (5, 10, 20)
REQUEST_TIMEOUT = 30

# Polling pace. GITHUB_TOKEN allows 1000 requests an hour per repository for every job together and a poll costs two,
# so a queued run is looked at once a minute and a running one (about two minutes) every 20 s. A head with no run listed
# is read 10 times, a minute apart: when Actions is slow the run can be created late.
RUNNING_POLL_SECONDS = 20
QUEUED_POLL_SECONDS = 60
SETTLING_READS = 10
SETTLING_SECONDS = 60


@dataclass
class Response:
    """status 0 is a failure before any answer (a refused or timed-out connection); body then holds the reason."""

    status: int
    body: str = ""
    headers: dict = field(default_factory=dict)


class ApiError(Exception):
    """An answer the wait cannot act on, or a fault that outlived its retries."""


def http_fetch(base, token):
    def fetch(path):
        request = urllib.request.Request(
            base.rstrip("/") + "/" + path.lstrip("/"),
            headers={
                "Accept": "application/vnd.github+json",
                "Authorization": "Bearer " + token,
                "User-Agent": "pig-wait-preflight",
                "X-GitHub-Api-Version": "2022-11-28",
            },
        )
        try:
            with urllib.request.urlopen(request, timeout=REQUEST_TIMEOUT) as answer:
                return Response(answer.status, answer.read().decode("utf-8", "replace"), dict(answer.headers))
        except urllib.error.HTTPError as err:
            return Response(err.code, err.read().decode("utf-8", "replace"), dict(err.headers))
        except (OSError, ValueError) as err:  # URLError, timeouts and resets are OSErrors; http.client errors are not
            return Response(0, str(err))
        except Exception as err:  # http.client.HTTPException and the like: a broken answer, not a bug of this script
            return Response(0, "%s: %s" % (type(err).__name__, err))

    return fetch


def is_fault(response):
    """A failure of GitHub or the network, not an answer about our request."""
    if response.status == 0 or response.status >= 500 or response.status == 429:
        return True
    if response.status == 403:  # primary and secondary rate limits answer 403
        headers = {k.lower(): v for k, v in response.headers.items()}
        return headers.get("x-ratelimit-remaining") == "0" or "retry-after" in headers or "rate limit" in response.body.lower()
    return False


class Api:
    def __init__(self, fetch, sleep):
        self.fetch = fetch
        self.sleep = sleep

    def get(self, path):
        """The answer for path; a fault is retried, and ApiError says what was last seen after the last try."""
        for attempt in range(FAULT_ATTEMPTS):
            response = self.fetch(path)
            if not is_fault(response):
                return response
            if attempt < FAULT_ATTEMPTS - 1:
                self.sleep(FAULT_BACKOFF[attempt])
        raise ApiError("GitHub API fault on GET %s after %d tries: HTTP %s %s" % (path, FAULT_ATTEMPTS, response.status, response.body[:200].strip()))

    def json(self, path):
        response = self.get(path)
        if response.status != 200:
            raise ApiError("GET %s: HTTP %d %s" % (path, response.status, response.body[:200].strip()))
        try:
            return json.loads(response.body)
        except ValueError as err:
            raise ApiError("GET %s: not JSON: %s" % (path, err)) from err


def workflow_missing_on_default_branch(api, repo, ref):
    """True only on an HTTP 404 for the workflow file on the default branch."""
    try:
        response = api.get("repos/%s/contents/%s?ref=%s" % (repo, WORKFLOW, urllib.parse.quote(ref, safe="")))
    except ApiError:
        return False
    return response.status == 404


def runs_of(api, repo, sha):
    query = urllib.parse.urlencode({"head_sha": sha, "event": EVENT, "per_page": 100})
    return api.json("repos/%s/actions/workflows/%s/runs?%s" % (repo, os.path.basename(WORKFLOW), query)).get("workflow_runs", [])


def status_of(api, repo, sha):
    """(state, description) of the newest "preflight" status of the head; ("none", "") when there is none."""
    for status in api.json("repos/%s/commits/%s/statuses?per_page=100" % (repo, sha)):  # newest first
        if status.get("context") == CONTEXT:
            return status.get("state") or "none", status.get("description") or ""
    return "none", ""


def describe(run):
    return "run %s (%s)" % (run.get("id"), run.get("html_url") or "no url")


def wait(api, repo, sha, log):
    """Poll until there is a verdict. Returns (ok, message)."""
    empty_reads = 0
    last = None

    def note(text):
        nonlocal last
        if text != last:
            last = text
            log(text)

    while True:
        runs = runs_of(api, repo, sha)  # before the status: see the module's comment
        state, description = status_of(api, repo, sha)
        if state == "success":
            return True, "pre-flight passed for %s" % sha
        if state in ("failure", "error"):
            return False, "the pre-flight rejected %s: %s; see the pre-flight comment on the pull request" % (sha, description or state)
        if state not in ("none", "pending"):
            return False, 'unknown pre-flight state "%s" for %s' % (state, sha)

        seen = "status %s%s" % (state, ": " + description if description else "")
        if not runs:
            empty_reads += 1
            if empty_reads >= SETTLING_READS:
                return False, "no pre-flight run for this head %s: none was created within %d minutes (%s); run the Pre-flight workflow for this pull request" % (
                    sha,
                    SETTLING_READS * SETTLING_SECONDS // 60,
                    seen,
                )
            note("no pre-flight run listed for %s yet (read %d of %d)" % (sha, empty_reads, SETTLING_READS))
            api.sleep(SETTLING_SECONDS)
            continue
        empty_reads = 0

        active = [run for run in runs if run.get("status") != "completed"]
        if not active:
            newest = max(runs, key=lambda run: (run.get("created_at") or "", run.get("id") or 0))
            return False, "pre-flight %s completed %s without a verdict for %s (%s); %s" % (
                describe(newest),
                newest.get("conclusion") or "with no conclusion",
                sha,
                seen,
                "the hourly pre-flight sweep sets the verdict; rerun this job after it" if state == "pending" else "run the Pre-flight workflow for this pull request",
            )

        running = [run for run in active if run.get("status") == "in_progress"]
        shown = running[0] if running else active[0]
        note("waiting for pre-flight %s: %s, %s" % (describe(shown), shown.get("status"), seen))
        api.sleep(RUNNING_POLL_SECONDS if running else QUEUED_POLL_SECONDS)


def main(argv, env, fetch=None, sleep=time.sleep, out=sys.stdout):
    if len(argv) != 3:
        print(__doc__.split("\n\n")[0], file=sys.stderr)
        print("usage: wait-preflight.py <owner/repo> <head-sha> <default-branch>", file=sys.stderr)
        return 2
    repo, sha, ref = argv
    if fetch is None:
        fetch = http_fetch(env.get("GITHUB_API_URL") or "https://api.github.com", env["GH_TOKEN"])
    api = Api(fetch, sleep)

    def log(text):
        print(text, file=out, flush=True)

    if workflow_missing_on_default_branch(api, repo, ref):
        notice = "pre-flight is not on the default branch yet: verdict skipped"
        log("::notice::" + notice)
        if env.get("GITHUB_STEP_SUMMARY"):
            with open(env["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as summary:
                summary.write("%s (%s is absent from %s)\n" % (notice, WORKFLOW, ref))
        return 0

    try:
        ok, message = wait(api, repo, sha, log)
    except ApiError as err:
        log("::error::%s" % err)
        return 1
    log(message if ok else "::error::" + message)
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:], os.environ))
