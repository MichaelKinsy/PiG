#!/usr/bin/env python3
"""Required behaviour-proof class per ledger row kind, and the rows that lack it. Deterministic: ledger data, the pinned inventory, Go test ASTs.

usage: proofclass.py [OUT_DIR] [--baseline FILE] [--update-baseline] [--enforce]   (repository root)

Required class by row kind (first match; the kind comes from the row id and the inventory kind):
  D  design-out      : disposition designed-out. Proven when the rationale cites a divergence/additive D-number that exists in docs/ OR a probe
                       (the rationale says what Pi code was read), AND the design-out audit columns (parity, maintainability, performance) exist
                       for the row in docs/parity/designout-audit.tsv. Without that file every design-out row is unproven (audit missing).
  W  wire            : a member of the extension API / extension context / RPC surface, or an event type: a per-SDK conformance row or a wiring
                       pass: an asserting Go test under test/extension-conformance, test/wiring, extensions/ or coding/extension/host that
                       names the Go member or the Pi member string.
  R  registration    : a member of a registration type: field survival: an asserting test in those directories whose name says
                       Registration/Survive/RoundTrip and that names the member.
  U  union           : a type alias that is a union (prompts, events, entries): an asserting test named Exhaustive/Variants/EveryKind/AllKinds
                       that names the union or its Go type.
  F  behaviour       : a function, class, constructor, or callable member: an asserting Go test that names the Go symbol AND is mutation-checked
                       (the test appears in a unit-evidence file with a mutation, or portmap-mutants.json has a mutant in the symbol's Go file) or
                       is a Pi-oracle test (it runs the pinned Node oracle).
  N  none            : data shapes (interfaces, non-union aliases, variables, data properties): no behaviour to prove.
Rows that are not closed are reported by the gap detector, not here.

Output: OUT_DIR/unproven-behaviour.tsv, a per-package table on stdout, and a ratchet against the committed baseline (count may only go down).
The ratchet is report-only unless --enforce.
"""
import json, os, re, subprocess, sys, collections, glob

args = [a for a in sys.argv[1:] if not a.startswith('--')]
OUT = args[0] if args else 'build/ledger-proof-classes'
BASELINE = 'test/parity/interface-closure/gaps/proof-class-baseline.tsv'
if '--baseline' in sys.argv:
    BASELINE = sys.argv[sys.argv.index('--baseline') + 1]
    args = [a for a in args if a != BASELINE]
HERE = os.path.dirname(os.path.abspath(__file__))
os.makedirs(OUT, exist_ok=True)
ver = sorted(re.findall(r'mapping-v(\d+\.\d+\.\d+)\.json', ' '.join(os.listdir('test/parity/interfaces'))), key=lambda v: tuple(map(int, v.split('.'))))[-1]
H = {x['id']: x for x in json.load(open(f'test/parity/interfaces/mapping-v{ver}.json'))['mappings']}
INV = {x['id']: x for x in json.load(open(f'test/parity/interfaces/upstream-v{ver}.json'))['interfaces']}

refs_path = os.path.join(OUT, 'test-refs.json')
exe = os.path.join(OUT, 'testrefs')
subprocess.check_call(['go', 'build', '-o', exe, './test/parity/interface-closure/breakdown/testrefs'])
with open(refs_path, 'w') as fh:
    subprocess.check_call([exe, '.'], stdout=fh)
refs = json.load(open(refs_path))

mutation_tests = set()
for f in glob.glob('test/parity/unit-evidence/*.json'):
    try:
        d = json.load(open(f))
    except ValueError:
        continue
    for e in d if isinstance(d, list) else []:
        if isinstance(e, dict) and e.get('mutation'):
            mutation_tests.update(e.get('tests') or [])
mutant_files = set()
try:
    for entries in json.load(open('test/parity/portmap-mutants.json')).values():
        mutant_files.update(m['file'] for m in entries)
except (OSError, ValueError):
    pass

divergences = set(re.findall(r'\bD\d+\b', ' '.join(open(p, errors='replace').read() for p in glob.glob('docs/**/*.md', recursive=True) if 'DIVERGENCE' in p.upper() or 'additive' in p.lower())))
audit = {}
if os.path.exists('docs/parity/designout-audit.tsv'):
    for ln in open('docs/parity/designout-audit.tsv').read().splitlines()[1:]:
        c = ln.split('\t')
        if len(c) >= 5 and all(c[2:5]):
            audit[c[0]] = c

WIRE_TOP = {'ExtensionAPI', 'ExtensionContext', 'ExtensionCommandContext', 'ExtensionUIContext', 'ExtensionActions', 'ExtensionContextActions',
            'ExtensionToolContext', 'ExtensionRuntime', 'ExtensionRunner', 'ExtensionFactory'}
REG_RE = re.compile(r'(Registration|Definition|Declaration|Registered\w*|ProviderConfig|ProviderModelConfig|ExtensionFlag|ExtensionShortcut)$')
EXT_DIRS = ('test/extension-conformance', 'test/wiring', 'extensions/', 'coding/extension/host', 'coding/extension')


def top_pipe(t):
    depth = 0
    for ch in t:
        if ch in '<([{':
            depth += 1
        elif ch in '>)]}':
            depth -= 1
        elif ch == '|' and depth == 0:
            return True
    return False


def split(i):
    m = re.match(r'(pkg:[^#]*|cli[^#]*)#?([^:]*)(.*)', i)
    return (m.group(1), m.group(2), m.group(3)) if m else ('?', '', '')


def pkgname(i):
    m = re.match(r'pkg:([^/]+)/', i)
    return m.group(1) if m else ('cli' if i.startswith('cli') else '?')


def kind(i, r):
    if r['disposition'] == 'designed-out':
        return 'D'
    _, top, rest = split(i)
    inv = INV.get(i, {})
    k = inv.get('kind', '')
    if (top in WIRE_TOP or top.startswith('Rpc') or top.endswith('Event')) and rest:
        return 'W'
    if REG_RE.search(top) and rest:
        return 'R'
    if k == 'type-alias' and top_pipe(inv.get('shape', {}).get('type', '')) or top_pipe(inv.get('shape', {}).get('aliasTarget', '')):
        return 'U'
    if k in ('function', 'class', 'construct-overload', 'call-overload') or rest.endswith('::construct:0') or '::call:' in rest:
        return 'F'
    if k == 'property' and any(j.startswith(i + '::call:') for j in (i + '::call:0',) if j in H):
        return 'F'
    return 'N'


def idents(i, r):
    tgt = (r.get('pigTargets') or [''])[0]
    sym = tgt.partition('#')[2]
    top, _, member = sym.partition('.')
    _, ptop, rest = split(i)
    pm = re.search(r'::property:([^:]+)', rest)
    names = {x for x in (member or top, top, pm.group(1) if pm else '') if x}
    ev = re.search(r'::call:(.+)$', rest)
    if ev and not ev.group(1).isdigit():
        names.add(ev.group(1))
    return tgt, top, member, names, ptop


def asserting(dirs, names, namefilter=None, only_dirs=None):
    out = []
    for d, r in refs.items():
        if only_dirs and not d.startswith(only_dirs):
            continue
        if not only_dirs and d not in dirs:
            continue
        for n in names:
            for h in r.get(n, []):
                t, _, tag = h.partition('|')
                if tag == 'a' and (namefilter is None or namefilter.search(t)):
                    out.append((d, t))
    return out


def proven(c, i, r):
    tgt, top, member, names, ptop = idents(i, r)
    tdir = os.path.dirname(tgt.partition('#')[0])
    evdirs = {os.path.dirname(e[5:].partition('#')[0]) for e in (r.get('evidence') or []) if e.startswith('test:')}
    if c == 'D':
        why = r.get('rationale', '')
        cited = [d for d in re.findall(r'\bD\d+\b', why)]
        reason = any(d in divergences for d in cited) or bool(re.search(r'probe|no Pi code|Pi does not|TypeScript|type-level|re-export|inherited|compile-time|declares only|is unset', why))
        if not reason:
            return 'no D-number or probe'
        return '' if i in audit else 'design-out audit columns missing'
    if c == 'N':
        return ''
    if c == 'W':
        return '' if asserting(None, names, None, EXT_DIRS) else 'no conformance or wiring test names the member'
    if c == 'R':
        return '' if asserting(None, names, re.compile(r'Registration|Surviv|RoundTrip|Keep'), EXT_DIRS) else 'no field-survival test names the member'
    if c == 'U':
        return '' if asserting(None, names | {ptop}, re.compile(r'Exhaustive|Variants|EveryKind|AllKinds|Union'), EXT_DIRS) else 'no exhaustive-handling test names the union'
    hits = asserting({tdir} | evdirs, {member or top} if member or top else set())
    if not hits:
        return 'no asserting Go test names the symbol'
    file_mut = tgt.partition('#')[0] in mutant_files
    for d, t in hits:
        if t in mutation_tests or file_mut or (t + '|a') in refs.get(d, {}).get('@oracle', []):
            return ''
    return 'tests touch the symbol but none is mutation-checked or a Pi-oracle test'


rows = []
tally = collections.defaultdict(collections.Counter)
for i, r in H.items():
    if r['disposition'] not in ('ported', 'designed-out'):
        continue
    c = kind(i, r)
    why = proven(c, i, r)
    tally[pkgname(i)][(c, bool(why))] += 1
    if why:
        rows.append((i, pkgname(i), c, why))
rows.sort()
with open(os.path.join(OUT, 'unproven-behaviour.tsv'), 'w') as fh:
    fh.write('id\tpackage\trequired class\treason\n')
    for x in rows:
        fh.write('\t'.join(x) + '\n')

per = collections.Counter(x[1] for x in rows)
bykind = collections.Counter(x[2] for x in rows)
req = collections.Counter()
for p, t in tally.items():
    for (c, u), n in t.items():
        req[c] += n
print('ledger proof classes: unproven behaviour', len(rows), 'of', sum(req.values()), 'closed rows')
print('  by required class (unproven/required): ' + ', '.join(f'{c} {bykind[c]}/{req[c]}' for c in 'DWRUFN'))
print('  per package: ' + ', '.join(f'{p} {per[p]}' for p in sorted(per)))
base = {}
if os.path.exists(BASELINE):
    for ln in open(BASELINE).read().splitlines()[1:]:
        p, n = ln.split('\t')
        base[p] = int(n)
if '--update-baseline' in sys.argv:
    with open(BASELINE, 'w') as fh:
        fh.write('package\tunproven\n')
        for p in sorted(per):
            fh.write(f'{p}\t{per[p]}\n')
    print('baseline written', BASELINE)
    base = dict(per)
worse = {p: (per[p], base.get(p, 0)) for p in per if per[p] > base.get(p, 0)}
print('  ratchet vs', BASELINE, ':', 'no package above its baseline' if not worse else 'ABOVE baseline: ' + ', '.join(f'{p} {a}>{b}' for p, (a, b) in sorted(worse.items())))
sys.exit(1 if worse and '--enforce' in sys.argv else 0)
