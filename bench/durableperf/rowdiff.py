import sqlite3, json, sys, re
a, b = sys.argv[1], sys.argv[2]
SKIP = {'timestamp','sessionId','at','createdAt','updatedAt','now','startedAt','endedAt','durationMs','requestedAt','settledAt','time','ts'}
def norm(v, key=''):
    if isinstance(v, dict): return {k: norm(x, k) for k, x in v.items()}
    if isinstance(v, list): return [norm(x, key) for x in v]
    if key in SKIP and isinstance(v, (int, float)): return 0
    if isinstance(v, str):
        if v.startswith('faux:'): return 'faux:*'
        if re.fullmatch(r'[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}', v): return '<uuid>'
        if re.fullmatch(r'[0-9a-f]{8}-?[0-9a-f-]{20,}', v): return '<uuid>'
    return v
def rows(path):
    db = sqlite3.connect('file:%s?mode=ro' % path, uri=True)
    out = {}
    tabs = [r[0] for r in db.execute("select name from sqlite_master where type='table' and name not like 'sqlite_%'")]
    for t in tabs:
        cols = [r[1] for r in db.execute('pragma table_info(%s)' % t)]
        rs = []
        for r in db.execute('select * from %s' % t):
            d = {}
            for c, v in zip(cols, r):
                if c in ('record','content') and isinstance(v, str):
                    try: v = norm(json.loads(v))
                    except Exception: pass
                d[c] = v
            rs.append(d)
        rs.sort(key=lambda d: json.dumps([d.get('id', d.get('document_id', 0)), d.get('seq', 0)], default=str))
        out[t] = rs
    return out
A, B = rows(a), rows(b)
bad = 0
for t in sorted(set(A) | set(B)):
    ra, rb = A.get(t, []), B.get(t, [])
    if len(ra) != len(rb):
        print(t, 'row count', len(ra), len(rb)); bad += 1; continue
    nd = 0
    for x, y in zip(ra, rb):
        if x != y:
            nd += 1
            if nd <= 2:
                for k in x:
                    if x[k] != y.get(k): print(t, 'id', x.get('id'), 'col', k, '\n  A', json.dumps(x[k])[:300], '\n  B', json.dumps(y[k])[:300]); break
    print(t, len(ra), 'rows,', nd, 'differ'); bad += nd
print('TOTAL DIFFERENCES', bad)
