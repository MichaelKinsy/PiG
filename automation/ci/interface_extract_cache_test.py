"""Tests for automation/ci/interface-extract-cache.sh, the content-keyed cache
wrapper around the interface extractor used by `make interface-inventory-drift`.

These tests stub out `node` with a deterministic fake extractor (a function of
its --source-root/--published-root file contents) so the suite runs fast and
without a real TypeScript toolchain, while still exercising the real caching
script end to end: key computation, hit/miss, forced bypass, and concurrent
publish safety.
"""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import textwrap
import unittest

KEY_RE = re.compile(r"\b([0-9a-f]{64})\b")

ROOT = Path(__file__).resolve().parents[2]
SCRIPT = ROOT / "automation" / "ci" / "interface-extract-cache.sh"

FAKE_NODE = textwrap.dedent(
    """\
    #!/usr/bin/env bash
    set -euo pipefail
    if [ "${1:-}" = "--version" ]; then
      echo "v99.0.0-fake"
      exit 0
    fi
    args=("$@")
    script=""
    for a in "${args[@]}"; do
      case "$a" in *.mjs) script="$a" ;; esac
    done
    get_opt() {
      local flag="--$1" n=${#args[@]}
      for ((i = 0; i < n; i++)); do
        if [ "${args[$i]}" = "$flag" ]; then echo "${args[$((i + 1))]}"; return; fi
      done
    }
    echo "call:$script" >> "$EXTRACT_CALL_LOG"
    hash_dir() {
      find -L "$1" -type f -print0 | sort -z | xargs -0 cat 2>/dev/null | shasum -a 256 | awk '{print $1}'
    }
    case "$script" in
      */extract-cli.mjs)
        out=$(get_opt out); src=$(get_opt source-root)
        mkdir -p "$(dirname "$out")"
        printf '{"cli":"%s"}\\n' "$(hash_dir "$src")" > "$out"
        ;;
      */extract.mjs)
        src=$(get_opt source-root); pub=$(get_opt published-root)
        sout=$(get_opt source-out); pout=$(get_opt published-out)
        mkdir -p "$(dirname "$sout")" "$(dirname "$pout")"
        printf '{"source":"%s"}\\n' "$(hash_dir "$src")" > "$sout"
        printf '{"published":"%s"}\\n' "$(hash_dir "$pub")" > "$pout"
        ;;
      *)
        echo "fake node: unrecognized script $script" >&2
        exit 1
        ;;
    esac
    """
)


class InterfaceExtractCacheTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        base = Path(self.tmp.name)

        self.bin = base / "bin"
        self.bin.mkdir()
        node = self.bin / "node"
        node.write_text(FAKE_NODE)
        node.chmod(0o700)

        paths = self._make_fixture_tree(base)
        self.source_root = paths["source_root"]
        self.published_root = paths["published_root"]
        self.extractor_dir = paths["extractor_dir"]

        self.cache_home = base / "cache"
        self.call_log = base / "calls.log"
        self.call_log.write_text("")

        self.env = dict(
            os.environ,
            PATH=str(self.bin) + os.pathsep + os.environ["PATH"],
            PIG_CACHE_HOME=str(self.cache_home),
            PIG_TMP=str(base),
            EXTRACT_CALL_LOG=str(self.call_log),
        )
        self.out_dir = base / "out"
        self.out_dir.mkdir()

    @staticmethod
    def _make_fixture_tree(base):
        """Builds a source/published/extractor tree under `base`, byte-for-byte
        the same regardless of where `base` lives, so it can stand in for two
        different worktree checkouts of the same content at different
        absolute paths."""
        source_root = base / "upstream"
        published_root = base / "published"
        extractor_dir = base / "extractor"
        (extractor_dir / "src").mkdir(parents=True)
        source_root.mkdir()
        published_root.mkdir()
        (source_root / "a.ts").write_text("export const a = 1;\n")
        (published_root / "a.js").write_text("exports.a = 1;\n")
        (extractor_dir / "src" / "extract.mjs").write_text("// fake\n")
        (extractor_dir / "src" / "extract-cli.mjs").write_text("// fake\n")
        (extractor_dir / "package-lock.json").write_text('{"lockfileVersion":1}\n')
        return {"source_root": source_root, "published_root": published_root, "extractor_dir": extractor_dir}

    def run_cache_for(self, source_root, published_root, extractor_dir, out_dir, env):
        out_dir.mkdir(parents=True, exist_ok=True)
        out_source = out_dir / "source.json"
        out_published = out_dir / "published.json"
        out_cli = out_dir / "cli.json"
        result = subprocess.run(
            [
                str(SCRIPT),
                str(source_root),
                str(published_root),
                "9.9.9",
                str(extractor_dir),
                str(out_source),
                str(out_published),
                str(out_cli),
            ],
            env=env,
            capture_output=True,
            text=True,
        )
        self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        return result, out_source, out_published, out_cli

    def run_cache(self, env=None):
        result, out_source, out_published, out_cli = self.run_cache_for(
            self.source_root, self.published_root, self.extractor_dir, self.out_dir, env or self.env
        )
        return (
            json.loads(out_source.read_text()),
            json.loads(out_published.read_text()),
            json.loads(out_cli.read_text()),
        )

    def call_count(self):
        return len([line for line in self.call_log.read_text().splitlines() if line])

    @staticmethod
    def extract_key(result):
        match = KEY_RE.search(result.stderr)
        assert match, f"no cache key found in stderr: {result.stderr!r}"
        return match.group(1)

    def test_cold_run_extracts_and_produces_expected_content(self):
        source, published, cli = self.run_cache()
        self.assertIn("source", source)
        self.assertIn("published", published)
        self.assertIn("cli", cli)
        # Real extraction ran (both extract.mjs and extract-cli.mjs).
        self.assertEqual(self.call_count(), 2)

    def test_warm_run_hits_cache_and_skips_extraction(self):
        self.run_cache()
        self.assertEqual(self.call_count(), 2)
        source, published, cli = self.run_cache()
        # No new calls recorded: the second run was served entirely from cache.
        self.assertEqual(self.call_count(), 2)
        self.assertIn("source", source)
        self.assertIn("published", published)
        self.assertIn("cli", cli)

    def test_changing_extractor_code_forces_extraction(self):
        self.run_cache()
        self.assertEqual(self.call_count(), 2)
        (self.extractor_dir / "src" / "extract.mjs").write_text("// fake v2\n")
        self.run_cache()
        self.assertEqual(self.call_count(), 4)

    def test_changing_pinned_source_forces_extraction(self):
        self.run_cache()
        self.assertEqual(self.call_count(), 2)
        (self.source_root / "a.ts").write_text("export const a = 2;\n")
        self.run_cache()
        self.assertEqual(self.call_count(), 4)

    def test_opt_out_env_var_forces_fresh_extraction(self):
        self.run_cache()
        self.assertEqual(self.call_count(), 2)
        bypass_env = dict(self.env, PIG_INTERFACE_EXTRACT_CACHE="0")
        self.run_cache(env=bypass_env)
        self.assertEqual(self.call_count(), 4)

    def test_key_is_identical_across_different_worktree_roots(self):
        # Every landing candidate and slice checks out the extractor/source
        # tree at a different absolute path. Two independent trees with
        # byte-identical content -- standing in for two worktrees -- must
        # compute the same cache key, or the cache never hits across
        # candidates. Use separate cache homes so both runs are cold misses
        # and the reported key reflects only the key computation, not reuse.
        base_a = Path(self.tmp.name) / "worktree-a" / "nested" / "checkout"
        base_b = Path(self.tmp.name) / "somewhere-else" / "worktree-b"
        fixture_a = self._make_fixture_tree(base_a)
        fixture_b = self._make_fixture_tree(base_b)

        env_a = dict(self.env, PIG_CACHE_HOME=str(base_a / "cache"))
        env_b = dict(self.env, PIG_CACHE_HOME=str(base_b / "cache"))
        result_a, *_ = self.run_cache_for(
            fixture_a["source_root"], fixture_a["published_root"], fixture_a["extractor_dir"], base_a / "out", env_a
        )
        result_b, *_ = self.run_cache_for(
            fixture_b["source_root"], fixture_b["published_root"], fixture_b["extractor_dir"], base_b / "out", env_b
        )
        self.assertIn("miss", result_a.stderr)
        self.assertIn("miss", result_b.stderr)
        self.assertEqual(
            self.extract_key(result_a),
            self.extract_key(result_b),
            f"different keys for identical content at different absolute paths:\n{result_a.stderr}\n{result_b.stderr}",
        )

    def test_warm_hit_from_a_second_worktree_with_identical_content(self):
        # Populate the cache from one absolute location, then hit it from a
        # second, differently-rooted checkout of the same content sharing the
        # same PIG_CACHE_HOME -- this is the actual integrator scenario: one
        # candidate warms the cache, a later candidate at a different
        # worktree path must reuse it.
        base_a = Path(self.tmp.name) / "first-worktree"
        base_b = Path(self.tmp.name) / "second" / "worktree" / "elsewhere"
        fixture_a = self._make_fixture_tree(base_a)
        fixture_b = self._make_fixture_tree(base_b)
        shared_cache = Path(self.tmp.name) / "shared-cache"
        env = dict(self.env, PIG_CACHE_HOME=str(shared_cache))

        result_a, source_a, published_a, cli_a = self.run_cache_for(
            fixture_a["source_root"], fixture_a["published_root"], fixture_a["extractor_dir"], base_a / "out", env
        )
        self.assertIn("miss", result_a.stderr)
        self.assertEqual(self.call_count(), 2)

        result_b, source_b, published_b, cli_b = self.run_cache_for(
            fixture_b["source_root"], fixture_b["published_root"], fixture_b["extractor_dir"], base_b / "out", env
        )
        self.assertIn("hit", result_b.stderr, result_b.stderr)
        # No new extraction: served entirely from the cache the first
        # worktree populated.
        self.assertEqual(self.call_count(), 2)
        self.assertEqual(self.extract_key(result_a), self.extract_key(result_b))
        self.assertEqual(json.loads(source_a.read_text()), json.loads(source_b.read_text()))
        self.assertEqual(json.loads(published_a.read_text()), json.loads(published_b.read_text()))
        self.assertEqual(json.loads(cli_a.read_text()), json.loads(cli_b.read_text()))

    def test_stale_committed_output_still_fails_comparison_on_warm_cache(self):
        # This mirrors what interface-inventory-drift does after calling the
        # cache script: cmp the (possibly cached) output against the
        # committed file. A warm cache must not let a stale committed
        # inventory pass.
        source, published, cli = self.run_cache()
        self.assertEqual(self.call_count(), 2)
        committed = self.out_dir / "committed-published.json"
        committed.write_text(json.dumps({"published": "stale-value"}) + "\n")
        # Second (warm) run: cache hit, but comparison against the stale
        # committed file must still fail.
        out_published = self.out_dir / "published.json"
        self.assertEqual(self.call_count(), 2)
        result = subprocess.run(
            ["cmp", str(out_published), str(committed)],
            capture_output=True,
        )
        self.assertNotEqual(result.returncode, 0)

    def test_concurrent_runs_do_not_corrupt_the_cache(self):
        procs = []
        outs = []
        for i in range(6):
            out_dir = Path(self.tmp.name) / f"concurrent-out-{i}"
            out_dir.mkdir()
            outs.append(out_dir)
            out_source = out_dir / "source.json"
            out_published = out_dir / "published.json"
            out_cli = out_dir / "cli.json"
            procs.append(
                subprocess.Popen(
                    [
                        str(SCRIPT),
                        str(self.source_root),
                        str(self.published_root),
                        "9.9.9",
                        str(self.extractor_dir),
                        str(out_source),
                        str(out_published),
                        str(out_cli),
                    ],
                    env=self.env,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                )
            )
        results = [p.communicate() for p in procs]
        for p, (out, err) in zip(procs, results):
            self.assertEqual(p.returncode, 0, out.decode() + err.decode())

        # Every concurrent caller got valid, matching JSON -- no truncation,
        # no interleaved bytes from another writer.
        expected_source = json.loads((outs[0] / "source.json").read_text())
        expected_published = json.loads((outs[0] / "published.json").read_text())
        expected_cli = json.loads((outs[0] / "cli.json").read_text())
        for out_dir in outs:
            self.assertEqual(json.loads((out_dir / "source.json").read_text()), expected_source)
            self.assertEqual(json.loads((out_dir / "published.json").read_text()), expected_published)
            self.assertEqual(json.loads((out_dir / "cli.json").read_text()), expected_cli)

        # Exactly one published cache entry exists for this key, and it holds
        # three well-formed files (not a nested stray directory from a
        # mishandled `mv` onto an existing directory).
        entries = list((self.cache_home / "pig-interface-extract").glob("*"))
        entries = [e for e in entries if not e.name.startswith(".")]
        self.assertEqual(len(entries), 1, entries)
        entry = entries[0]
        self.assertTrue(entry.is_dir())
        children = sorted(p.name for p in entry.iterdir())
        self.assertEqual(children, ["cli.json", "published.json", "source.json"])
        for name in children:
            json.loads((entry / name).read_text())  # parses cleanly, not truncated


if __name__ == "__main__":
    unittest.main()
