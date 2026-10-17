// runner/nightly.sh's native-build step: the build gets the tree's own Go release and workspace (GOWORK and GOTOOLCHAIN
// unset, whatever the caller exports), and a failed checkout or build ends the night with exit 1 and a message naming it,
// so the cron wrapper cannot post an empty night. The script runs from a temporary copy beside a track-row.sh stub, on a
// throwaway git repository whose build-native.sh stub records its environment.
import assert from "node:assert/strict"
import { spawnSync } from "node:child_process"
import { chmodSync, copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { dirname, join } from "node:path"
import test from "node:test"
import { fileURLToPath } from "node:url"

const runner = join(dirname(fileURLToPath(import.meta.url)), "..", "runner")

function fixture(t) {
	const dir = mkdtempSync(join(tmpdir(), "nightly-sh-"))
	t.after(() => rmSync(dir, { recursive: true, force: true }))
	const env = { PATH: process.env.PATH, HOME: dir, GIT_CONFIG_GLOBAL: "/dev/null", GIT_CONFIG_NOSYSTEM: "1", GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@t", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@t" }
	const git = (cwd, ...args) => {
		const r = spawnSync("git", args, { cwd, env, encoding: "utf8" })
		assert.equal(r.status, 0, r.stderr)
		return r.stdout.trim()
	}
	const bin = join(dir, "runner")
	mkdirSync(bin)
	copyFileSync(join(runner, "nightly.sh"), join(bin, "nightly.sh"))
	writeFileSync(join(bin, "track-row.sh"), "#!/bin/sh\necho '{}'\n")
	const origin = join(dir, "origin")
	mkdirSync(join(origin, "durable/core/budget"), { recursive: true })
	const build = join(origin, "durable/core/budget/build-native.sh")
	writeFileSync(build, `#!/bin/sh\necho "GOWORK=\${GOWORK-unset} GOTOOLCHAIN=\${GOTOOLCHAIN-unset}" > "${join(dir, "build-env.txt")}"\necho "go: build failed here"\nexit 1\n`)
	chmodSync(build, 0o755)
	writeFileSync(join(origin, "file.txt"), "one\n")
	git(origin, "init", "-q", "-b", "main")
	git(origin, "add", ".")
	git(origin, "commit", "-q", "-m", "one")
	const source = join(dir, "source")
	git(dir, "clone", "-q", "-o", "staging", origin, source)
	const out = join(dir, "out")
	const run = (extra = {}) => spawnSync("bash", [join(bin, "nightly.sh")], {
		env: { ...env, BRANCH: "main", BENCH: dir, PIG_SOURCE: source, OUT: out, BASELINE: join(dir, "baseline.json"), NATIVE_FIXTURES: dir, ...extra },
		encoding: "utf8",
	})
	return { dir, origin, source, git, run }
}

test("the native build runs without the caller's GOWORK and GOTOOLCHAIN, and its failure ends the night with exit 1", t => {
	const f = fixture(t)
	const sha = f.git(f.origin, "rev-parse", "--short=10", "HEAD")
	const r = f.run({ GOWORK: "off", GOTOOLCHAIN: "go1.0" })
	assert.equal(r.status, 1, r.stderr)
	assert.equal(readFileSync(join(f.dir, "build-env.txt"), "utf8").trim(), "GOWORK=unset GOTOOLCHAIN=unset")
	assert.match(r.stderr, new RegExp(`nightly\\.sh: the native build of ${sha} failed: go: build failed here`))
	assert.equal(r.stdout, "")
})

test("a checkout that fails is reported as a checkout, not as a build", t => {
	const f = fixture(t)
	writeFileSync(join(f.origin, "file.txt"), "two\n")
	f.git(f.origin, "commit", "-q", "-am", "two")
	writeFileSync(join(f.source, "file.txt"), "local edit\n") // a local change the checkout of "two" would overwrite
	const sha = f.git(f.origin, "rev-parse", "--short=10", "HEAD")
	const r = f.run()
	assert.equal(r.status, 1, r.stderr)
	assert.match(r.stderr, new RegExp(`nightly\\.sh: cannot check out ${sha} in `))
	assert.doesNotMatch(r.stderr, /native build/)
	assert.throws(() => readFileSync(join(f.dir, "build-env.txt")), { code: "ENOENT" })
})
