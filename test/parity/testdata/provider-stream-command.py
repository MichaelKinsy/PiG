import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

root=Path(__file__).resolve().parents[3]
kind=sys.argv[1]
binary=(os.environ.get('PIG_PARITY_PIG_BIN') or os.environ.get('PIG_BIN') or shutil.which('pig')) if kind=='pig' else (os.environ.get('PIG_PARITY_PI_BIN') or os.environ.get('PI_BIN') or shutil.which('pi'))
producer=root/'coding/testdata/provider-stream-producer.mjs'
consumer=root/'coding/testdata/provider-stream-consumer.mjs'
rows=[]
for method in ['stream','streamSimple']:
 with tempfile.TemporaryDirectory(prefix='provider-proof-') as directory:
  session=Path(directory)/'session.jsonl'
  usage={"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}
  seed=[{"type":"session","version":3,"id":"provider-proof-session","timestamp":"2026-01-01T00:00:00.000Z","cwd":directory},{"type":"message","id":"seed","parentId":None,"timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"assistant","api":"openai-responses","provider":"openai","model":"gpt-4.1","content":[{"type":"text","text":"seed"}],"stopReason":"stop","usage":usage,"timestamp":1}}]
  session.write_text(''.join(json.dumps(entry)+'\n' for entry in seed))
  agent_dir=Path(directory)/'agent';agent_dir.mkdir()
  env=dict(os.environ,PIG_CODING_AGENT_DIR=str(agent_dir),PI_CODING_AGENT_DIR=str(agent_dir),OPENAI_API_KEY='fixture',PI_OFFLINE='1',PI_SKIP_VERSION_CHECK='1')
  run=subprocess.run([binary,'-p','--no-extensions','--session',str(session),'--model','openai/gpt-4.1','-e',str(producer),'-e',str(consumer),'/stream-custom '+method+':proof'],cwd=directory,env=env,text=True,capture_output=True,timeout=30)
  if run.returncode: raise RuntimeError(run.stderr+run.stdout)
  entries=[json.loads(line) for line in session.read_text().splitlines()]
  proof=[entry['data'] for entry in entries if entry.get('customType')=='provider-proof']
  if len(proof)!=1: raise RuntimeError('missing provider command proof: '+run.stderr+run.stdout+' entries='+json.dumps(entries))
  rows.append(proof[0])
print(json.dumps(rows,separators=(',',':'),sort_keys=True))
