#!/usr/bin/env python3
"""Row closure history: for every ledger row, the first commit (topological order, oldest first) whose mapping file has it ported/designed-out."""
import json,subprocess,sys
BASE=sys.argv[1]; HEAD=sys.argv[2]; OUT=sys.argv[3]
FILES=['test/parity/interfaces/mapping-v1.0.4.json','test/parity/interfaces/mapping-v1.1.0.json']
revs=subprocess.check_output(['git','log','--reverse','--topo-order','--format=%H %ct',f'{BASE}..{HEAD}','--']+FILES,text=True).split('\n')
revs=[r.split() for r in revs if r]
first={}   # id -> record at first closure
seen=set()
cat=subprocess.Popen(['git','cat-file','--batch'],stdin=subprocess.PIPE,stdout=subprocess.PIPE,text=False)
def blob(spec):
    cat.stdin.write((spec+'\n').encode()); cat.stdin.flush()
    h=cat.stdout.readline().split()
    if len(h)<3: return None
    data=cat.stdout.read(int(h[2])); cat.stdout.read(1); return data
n=0
for sha,ct in revs:
    for f in FILES:
        b=blob(f'{sha}:{f}')
        if b is None: continue
        key=hash(b)
        if (f,key) in seen: continue
        seen.add((f,key))
        try: rows=json.loads(b)['mappings']
        except ValueError:
            print('unparseable',sha[:10],f,file=sys.stderr); continue
        for x in rows:
            if x['disposition'] in('ported','designed-out') and x['id'] not in first:
                first[x['id']]={'sha':sha[:10],'ct':int(ct),'disp':x['disposition'],'rat':(x.get('rationale') or '')[:200],'ev':bool(x.get('evidence')),'tgt':x.get('pigTargets',[])[:2]}
    n+=1
    if n%100==0: print(n,len(revs),len(first),file=sys.stderr,flush=True)
json.dump(first,open(OUT,'w'))
print('rows with a first-closure record',len(first))
