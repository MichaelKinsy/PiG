#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT"

echo '=== go test scheduler ==='
"$ROOT/automation/ci/test-grouped.sh" report

echo
echo '=== parity scheduler ==='
python3 <<'PY'
import os, re, collections, json
root='parity/scenarios'
limits = os.environ.get('PARITY_GROUP_LIMITS', 'process=12,tmux=12,rpc=6')
results_path = os.environ.get('RESULTS') or os.path.join(os.environ.get('TMPDIR') or '/tmp', 'parity-results.json')

def group_for(driver, tags, explicit):
    if 'serial' in tags:
        return 'exclusive'
    if explicit:
        return explicit
    if driver == 'interactive-tmux':
        return 'tmux'
    if driver == 'rpc-mode':
        return 'rpc'
    return 'process'

counts = collections.Counter()
examples = collections.defaultdict(list)
overrides = []
for dp,_,fs in os.walk(root):
    for f in fs:
        if not f.endswith('.toml'):
            continue
        p = os.path.join(dp,f)
        s = open(p).read()
        dm = re.search(r'^driver\s*=\s*"([^"]+)"', s, re.M)
        driver = dm.group(1) if dm else 'unknown'
        gm = re.search(r'^group\s*=\s*"([^"]+)"', s, re.M)
        explicit = gm.group(1) if gm else ''
        tm = re.search(r'^tags\s*=\s*\[(.*?)\]', s, re.S|re.M)
        tags = re.findall(r'"([^"]+)"', tm.group(1)) if tm else []
        g = group_for(driver, tags, explicit)
        rel = os.path.relpath(p, root)
        counts[g] += 1
        if explicit:
            overrides.append((rel, driver, explicit))
        if len(examples[g]) < 3:
            examples[g].append(rel)
print(f'group-limits: {limits}')
for g in ['process', 'tmux', 'rpc', 'exclusive']:
    n = counts[g]
    if not n:
        continue
    print(f'  {g:10} {n:3} scenarios')
    for ex in examples[g]:
        print(f'    - {ex}')
if overrides:
    print('\n  explicit overrides:')
    for rel, driver, explicit in overrides:
        print(f'    - {rel}: driver={driver} -> group={explicit}')
else:
    print('\n  explicit overrides: none')

if os.path.exists(results_path):
    try:
        data = json.load(open(results_path))
    except Exception as e:
        print(f'\nresults: could not read {results_path}: {e}')
    else:
        print(f'\nresults: {results_path}')
        print('  note: ms is wall-time-to-ready; under heavy parallel load')
        print('        a scenario can show large ms purely from CPU/IO contention.')
        print('        Re-run a slow scenario with -parallel 1 before treating it')
        print('        as a real hotspot.')
        by_group = collections.defaultdict(list)
        explicit = []
        for row in data:
            grp = row.get('group') or group_for(row.get('driver') or '', row.get('tags') or [], '')
            by_group[grp].append(row)
        for grp in sorted(by_group):
            rows = sorted(by_group[grp], key=lambda r: max(r.get('pig_median_ms', 0), r.get('pi_median_ms', 0)), reverse=True)[:3]
            print(f'  slowest {grp}:')
            for r in rows:
                mx = max(r.get('pig_median_ms', 0), r.get('pi_median_ms', 0))
                print(f'    - {r.get("name")}: {mx}ms (driver={r.get("driver")})')
PY
