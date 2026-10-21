# SPDX-License-Identifier: MIT
import os,re,subprocess,shutil,collections
root=os.path.expanduser("~/phi-cmp/binsize-nodetree")
img=(".png",".jpg",".jpeg",".svg",".gif",".ico")
def cls(rel):
    parts=rel.split("/"); low=rel.lower(); n=parts[-1].lower()
    if n.endswith((".d.ts",".d.mts",".d.cts")): return "dts"
    if n.endswith(".map"): return "map"
    if n.endswith(img) and any(p.lower() in("docs","doc") for p in parts[:-1]): return "docimg"
    if any(p.lower() in("example","examples") for p in parts[:-1]): return "examples"
    return None
tot=collections.Counter(); cnt=collections.Counter(); keep=[]; drop=[]
allf=[]
for d,_,fs in os.walk(root):
    for f in fs:
        p=os.path.join(d,f); rel=os.path.relpath(p,root); s=os.path.getsize(p)
        c=cls(rel); allf.append((rel,s,c))
        if c: tot[c]+=s; cnt[c]+=1; drop.append(rel)
        else: keep.append(rel)
print("raw total",sum(s for _,s,_ in allf),"files",len(allf))
for c in tot: print(c,cnt[c],tot[c])
print("pruned bytes",sum(tot.values()))
# other candidates by extension in kept
ext=collections.Counter(); 
for rel,s,c in allf:
    if not c: ext[os.path.splitext(rel)[1] or "(none)"]+=s
print("kept by ext",ext.most_common(12))
os.chdir(root)
def tarsize(files,cmd):
    open("/tmp/binsize-list","w").write("\n".join(sorted(files))+"\n")
    p=subprocess.run(f"tar --no-recursion -T /tmp/binsize-list -cf - | {cmd} | wc -c",shell=True,capture_output=True,text=True)
    return int(p.stdout)
allrel=[r for r,_,_ in allf]
for name,files in(("full",allrel),("pruned",keep)):
    raw=sum(s for r,s,c in allf if (name=="full" or not c))
    print(name,"raw",raw,"tar|zstd-19",tarsize(files,"zstd -19 -T1 -c --long=27"),"tar|zstd-19(no long)",tarsize(files,"zstd -19 -T1 -c"),"tar|gzip-9",tarsize(files,"gzip -9 -c"))
os.remove("/tmp/binsize-list")
# per category compressed
for c in tot:
    fl=[r for r,_,cc in allf if cc==c]; print(c,"tar|zstd-19",tarsize(fl,"zstd -19 -T1 -c"))
