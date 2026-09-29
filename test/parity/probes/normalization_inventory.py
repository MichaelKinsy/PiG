#!/usr/bin/env python3
"""Inventory every scenario normalization and crop; fail on unexplained rules or drift."""
import argparse
import hashlib
import json
from pathlib import Path
import tomllib


MECHANISMS = {
    "test/parity/runner/asserts.go": "Normalized/layout comparators are explicit plain-terminal contracts: ANSI, CR, trailing spaces and outer blank rows are not compared. They are not full wire proof. Exact comparators apply only scenario-declared, justified replacements.",
    "test/parity/runner/changelog_assert.go": "D22: the products ship different changelog documents. Compare every rendered release header, in order, against that binary's independent source document; this does not claim cross-product equality of body text or styling.",
    "test/parity/runner/default_cwd.go": "D2/D22 fixture control balances prompt paths. Only terminal captures use length-preserving cwd digit substitution; process records retain raw paths unless an explicit JSON alias selects them.",
    "test/parity/runner/driver_rpc.go": "canonical_json sorts JSON object keys only and preserves terminal framing. Raw stdout and stderr are retained. wait_contains/wait_event are new-output barriers, not acceptance assertions.",
    "test/parity/runner/json_compare.go": "Parsed full records ignore object-member order and equivalent JSON number spelling only. Explicit scalar aliases preserve field presence, types, ordered arrays and bijective ID references; no ignored-field list exists.",
    "test/parity/runner/driver_tmux.go": "Terminal state capture is not a byte-stream assertion. A scenario explicitly selects viewport or retained history and its crop. History capture uses session-local maximum retention without changing pane size or shared defaults; required anchors and source-derived assertions reject incomplete history. Readiness and new-output barriers are synchronization, not behavioral coverage.",
    "test/parity/runner/driver_ht.go": "Headless terminal state capture compares the declared visible surface, not transient bytes. Tool stderr is diagnostic transport noise, not product stderr.",
    "test/parity/runner/footer_crop.go": "Footer scenarios compare the complete owned footer rows rather than unrelated transcript rows; crop configuration is inventoried per scenario.",
    "test/parity/testdata/provider-wire-pi.mjs": "Legacy role/key/auth summaries are diagnostic only: wireBody now compares the entire captured HTTP request body alongside them. Aggregated provider response summaries do not prove mode events; real-provider RPC/JSON scenarios compare those separately.",
    "test/parity/testdata/provider-wire-pig/main.go": "Legacy role/key/auth summaries are diagnostic only: wireBody now compares the entire captured HTTP request body alongside them. Aggregated provider response summaries do not prove mode events; real-provider RPC/JSON scenarios compare those separately.",
    "test/parity/testdata/list-limit-number-pi.mjs": "This numeric-renderer probe selects the complete warning-bracket text from Pi's actual renderResult output under an explicit unstyled fixture theme. It claims number/notice text only, not terminal layout or ANSI styling; full themed body rows are checked by the Go caller regression.",
    "test/parity/testdata/list-limit-number-pig.sh": "The wrapper retains every numeric warning observation and removes only Go test-framework framing after requiring successful test completion. Failed tests retain their diagnostics and nonzero exit status.",
    "test/parity/testdata/npm-registry-scope.py": "This D79 probe measures metadata lookup cwd, selected-command argv, registry traffic and absence of reinstallation. It asserts the complete expected command sequence and exactly one package request before reporting scope labels. It does not claim complete CLI progress/stdout/stderr equality; failed commands retain their captured diagnostics.",
    "test/parity/testdata/rpc-wire-drift.py": "This probe emits complete mutation, UI, recovery, compaction, fork, switch and initial-state records without identity rewriting. The scenario's shared typed JSON comparator owns aliases. Other prompt events are outside these focused contracts and the complete RPC stream is captured as raw.json, not discarded. The fixture selects an explicit session directory, but generated filename syntax remains checked.",
    "test/parity/testdata/session-collision-pi.mjs": "This entry-allocation probe controls seed/duplicate/fresh entropy in the real Pi SessionManager and compares IDs, parent links, retained indexed content and generator calls. It makes no timestamp or whole-record claim; those contracts are covered by the Session wire probe.",
    "test/parity/testdata/session-collision-pig.sh": "The wrapper requires the production Session collision test to succeed and retains its complete observation line. It removes only Go test-framework framing; a mutation failure remains a nonzero process result.",
    "test/parity/testdata/session-wire.py": "The Session query probe validates unique eight-hex generated IDs and exact millisecond ISO timestamps before injective substitution. Imported IDs/times remain unchanged; only reconstructed label identities are aliased after collision checks. Complete get_entries records are compared, not unrelated mutation events. Signal probes compare process wait status after real provider/command barriers, not terminal rendering.",
    "test/parity/testdata/test-faux-provider.ts": "Canned followups exercise specific orchestration/rendering questions, not complete result metadata. Full RPC tool records and real-provider mode scenarios supply result acceptance independently of final prose.",
}


def inventory(root):
    scenarios = []
    for path in sorted((root / "test/parity/scenarios").rglob("*.toml")):
        text = path.read_text()
        data = tomllib.loads(text)
        assertions = data.get("assert", {})
        transforms = []
        for kind in ("normalize_replace", "json_aliases"):
            for rule in assertions.get(kind, []):
                if not rule.get("reason", "").strip():
                    raise ValueError(f"{path}: {kind} requires a rule-level reason")
                transforms.append({"kind": kind, **rule})
        if assertions.get("json_output_equal") and (assertions.get("output_normalized_equal") or assertions.get("normalize_replace")):
            raise ValueError(f"{path}: JSON comparison cannot use text normalization")
        for key in ("output_normalized_equal", "output_layout_equal", "artifact_normalized_equal"):
            if assertions.get(key):
                transforms.append({"kind": key, "reason": "Declared plain-text/layout surface: " + data["description"], "limitations": "ANSI, CR, trailing spaces and outer blank rows are not equality evidence."})
        if data.get("rpc", {}).get("canonical_json"):
            transforms.append({"kind": "canonical_json", "reason": "JSON object member order is not semantic. Raw stdout remains retained; fields and record order are not projected."})
        if assertions.get("changelog_headers_complete"):
            transforms.append({"kind": "changelog_headers_complete", "reason": "D22: compare the complete source-reversed release-header sequence against each binary's own bundled document. No release header is aliased, dropped, deduplicated or sorted.", "limitations": "This checks document header selection and ordering, not cross-product body or styling equality."})
        tmux = data.get("tmux", {})
        crop = {key: value for key, value in tmux.items() if key.startswith("capture_")}
        if crop:
            transforms.append({"kind": "terminal_crop", "configuration": crop, "reason": "Owned visible surface: " + data["description"], "limitations": "No claim about output outside this crop or transient terminal bytes."})
        if data.get("driver") in ("interactive-tmux", "headless-terminal"):
            transforms.append({"kind": "cwd_snapshot_digits", "reason": "D2: per-run cwd digits vary; preserve digit count so path width, truncation and surrounding cells remain compared."})
        if transforms:
            scenarios.append({"scenario": str(path.relative_to(root)), "transforms": transforms})
    mechanisms = [{"source": source, "sha256": hashlib.sha256((root / source).read_bytes()).hexdigest(), "reason": reason} for source, reason in sorted(MECHANISMS.items())]
    return {"mechanisms": mechanisms, "scenarios": scenarios}


def render(root):
    return json.dumps(inventory(root), ensure_ascii=False, indent=2) + "\n"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path(__file__).resolve().parents[3])
    parser.add_argument("--check", action="store_true")
    args = parser.parse_args()
    target = args.root / "test/parity/normalization-inventory.json"
    content = render(args.root)
    if args.check:
        if not target.exists() or target.read_text() != content:
            raise SystemExit("normalization inventory drift: review the changes, then run python3 test/parity/probes/normalization_inventory.py")
    else:
        target.write_text(content)


if __name__ == "__main__":
    main()
