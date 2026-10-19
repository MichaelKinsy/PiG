"""SessionMirror reads after an appended entry cost the new entry, not the log.

An append below the cached leaf extends the cached branch; a leaf anywhere else (fork, rewind, another session) rebuilds it.
"""

from __future__ import annotations

import random
from typing import Any

from pig_sdk import SessionMirror


def _entry(entry_id: str, parent: str | None) -> dict[str, Any]:
    return {"type": "message", "id": entry_id, "parentId": parent, "timestamp": "t", "message": {"role": "user", "content": entry_id}}


def _mirror() -> SessionMirror:
    mirror = SessionMirror()
    mirror.subscribed = True
    return mirror


def _push(mirror: SessionMirror, session: str, entries: list[dict[str, Any]], total: int, leaf: str) -> None:
    mirror.apply_update({"sessionId": session, "leafId": leaf, "entriesAppended": entries, "entryCount": total})


def _reference(log: list[dict[str, Any]], leaf: str) -> list[dict[str, Any]]:
    by_id = {entry["id"]: entry for entry in log}
    path: list[dict[str, Any]] = []
    current = by_id.get(leaf)
    while current is not None:
        path.append(current)
        current = by_id.get(current["parentId"]) if current["parentId"] else None
    path.reverse()
    return path


def test_incremental_branch_matches_an_uncached_walk_across_leaf_moves() -> None:
    rng = random.Random(7)
    mirror = _mirror()
    session, log, leaf = "s1", [], ""
    for step in range(400):
        op = rng.randrange(10)
        if op < 5 or not log:
            entry = _entry(f"{session}-e{len(log)}", leaf or None)
            _push(mirror, session, [entry], len(log) + 1, entry["id"])
            log.append(entry)
            leaf = entry["id"]
        elif op == 5:
            batch, parent = [], leaf
            for k in range(rng.randrange(1, 5)):
                entry = _entry(f"{session}-e{len(log) + k}", parent or None)
                batch.append(entry)
                parent = entry["id"]
            _push(mirror, session, batch, len(log) + len(batch), parent)
            log.extend(batch)
            leaf = parent
        elif op < 8:
            parent = rng.choice(log)["id"]
            entry = _entry(f"{session}-e{len(log)}", parent)
            _push(mirror, session, [entry], len(log) + 1, entry["id"])
            log.append(entry)
            leaf = entry["id"]
        elif op < 9:
            leaf = rng.choice(log)["id"]
            _push(mirror, session, [], len(log), leaf)
        else:
            session = f"s{step}"
            entry = _entry(f"{session}-e0", None)
            _push(mirror, session, [entry], 1, entry["id"])
            log, leaf = [entry], entry["id"]
        branch = mirror.get_branch()
        want = _reference(log, leaf)
        assert [e["id"] for e in branch] == [e["id"] for e in want], step
        assert all(got is expected for got, expected in zip(branch, want)), step


def test_a_side_entry_keeps_the_branch_and_a_repeated_id_rebuilds_it() -> None:
    mirror = _mirror()
    _push(mirror, "s", [_entry("a", None), _entry("b", "a")], 2, "b")
    assert [e["id"] for e in mirror.get_branch()] == ["a", "b"]
    _push(mirror, "s", [_entry("side", "a")], 3, "b")
    assert [e["id"] for e in mirror.get_branch()] == ["a", "b"]
    _push(mirror, "s", [_entry("b", "side")], 4, "b")
    assert [e["id"] for e in mirror.get_branch()] == ["a", "side", "b"]


def test_returned_branches_are_copies() -> None:
    mirror = _mirror()
    _push(mirror, "s", [_entry("a", None)], 1, "a")
    mirror.get_branch().clear()
    _push(mirror, "s", [_entry("b", "a")], 2, "b")
    assert [e["id"] for e in mirror.get_branch()] == ["a", "b"]


class _CountingIndex(dict):
    lookups = 0

    def get(self, key: Any, default: Any = None) -> Any:
        _CountingIndex.lookups += 1
        return super().get(key, default)


def _lookups_per_append_and_read(n: int) -> int:
    mirror = _mirror()
    log = [_entry(f"e{i}", f"e{i - 1}" if i else None) for i in range(n)]
    _push(mirror, "s", log, n, f"e{n - 1}")
    mirror.get_branch()
    mirror._index = _CountingIndex(mirror._index)
    best = None
    for at in range(n, n + 5):
        before = _CountingIndex.lookups
        _push(mirror, "s", [_entry(f"e{at}", f"e{at - 1}")], at + 1, f"e{at}")
        assert len(mirror.get_branch()) == at + 1
        cost = _CountingIndex.lookups - before
        best = cost if best is None else min(best, cost)
    return best or 0


def test_reading_the_branch_after_an_append_does_not_walk_the_log() -> None:
    small, large = _lookups_per_append_and_read(2000), _lookups_per_append_and_read(16000)
    assert large <= small + 4 and large < 16, (small, large)
