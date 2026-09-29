import http.server
import json
import os
from pathlib import Path
import select
import shutil
import socket
import socketserver
import subprocess
import sys
import tempfile
import threading

kind=sys.argv[1]
binary=(os.environ.get('PIG_PARITY_PIG_BIN') or os.environ.get('PIG_BIN') or shutil.which('pig')) if kind=='pig' else (os.environ.get('PIG_PARITY_PI_BIN') or os.environ.get('PI_BIN') or shutil.which('pi'))
if not binary: raise RuntimeError('Pinned CLI binary is not configured')
results=[]
for api in ['openai-completions','openai-responses']:
    class Origin(http.server.BaseHTTPRequestHandler):
        def log_message(self,*args): pass
        def do_POST(self):
            self.rfile.read(int(self.headers['Content-Length']))
            self.send_response(200); self.send_header('Content-Type','text/event-stream'); self.end_headers()
            if api=='openai-completions':
                frames=[{'id':'response','choices':[{'index':0,'delta':{'role':'assistant','content':'origin'},'finish_reason':None}]}, {'id':'response','choices':[{'index':0,'delta':{},'finish_reason':'stop'}],'usage':{'prompt_tokens':2,'completion_tokens':1,'total_tokens':3}}]
            else:
                item={'id':'message','type':'message','role':'assistant','status':'completed','content':[{'type':'output_text','text':'origin','annotations':[]}]}
                frames=[{'type':'response.output_item.added','output_index':0,'item':{**item,'content':[]}},{'type':'response.output_text.delta','output_index':0,'content_index':0,'delta':'origin'},{'type':'response.output_item.done','output_index':0,'item':item},{'type':'response.completed','response':{'id':'response','status':'completed','output':[item],'usage':{'input_tokens':2,'output_tokens':1,'total_tokens':3}}}]
            for frame in frames: self.wfile.write(('data: '+json.dumps(frame)+'\n\n').encode())
            if api=='openai-completions': self.wfile.write(b'data: [DONE]\n\n')
    origin=http.server.ThreadingHTTPServer(('127.0.0.1',0),Origin)
    target='127.0.0.1:'+str(origin.server_port)
    observations=[]
    class Proxy(socketserver.StreamRequestHandler):
        def handle(self):
            parts=self.rfile.readline().decode().strip().split(' ')
            observations.append({'method':parts[0],'targetIsOrigin':parts[1]==target,'protocol':parts[2]})
            while self.rfile.readline() not in (b'\r\n',b''): pass
            if parts[0]!='CONNECT': self.wfile.write(b'HTTP/1.1 501 Not Implemented\r\nContent-Length: 0\r\n\r\n');return
            with socket.create_connection(origin.server_address) as upstream:
                self.wfile.write(b'HTTP/1.1 200 Connection Established\r\n\r\n');self.wfile.flush()
                while True:
                    readable,_,_=select.select([self.connection,upstream],[],[],10)
                    if not readable: return
                    for source in readable:
                        data=source.recv(65536)
                        if not data: return
                        (upstream if source is self.connection else self.connection).sendall(data)
    proxy=socketserver.ThreadingTCPServer(('127.0.0.1',0),Proxy)
    workers=[threading.Thread(target=server.serve_forever) for server in (origin,proxy)]
    for worker in workers: worker.start()
    try:
        with tempfile.TemporaryDirectory(prefix='proxy-print-') as folder:
            directory=Path(folder)
            (directory/'settings.json').write_text(json.dumps({'httpProxy':'http://127.0.0.1:'+str(proxy.server_address[1])}))
            (directory/'models.json').write_text(json.dumps({'providers':{'fixture':{'api':api,'baseUrl':'http://'+target+'/v1','apiKey':'fixture','models':[{'id':'test','name':'Test','reasoning':False,'input':['text'],'cost':{'input':0,'output':0,'cacheRead':0,'cacheWrite':0},'contextWindow':10000,'maxTokens':1000}]}}}))
            env={key:value for key,value in os.environ.items() if key not in ['HTTP_PROXY','HTTPS_PROXY','http_proxy','https_proxy','NO_PROXY','no_proxy']}
            env.update({'PIG_CODING_AGENT_DIR':folder,'PI_CODING_AGENT_DIR':folder,'PI_OFFLINE':'1','PI_SKIP_VERSION_CHECK':'1'})
            run=subprocess.run([binary,'-p','--no-extensions','--model','fixture/test','hi'],cwd=folder,env=env,text=True,capture_output=True,timeout=30)
            if run.returncode: raise RuntimeError(api+': '+run.stderr+run.stdout)
            if not observations: raise RuntimeError('origin bypassed configured proxy')
            if run.stdout!='origin\n': raise RuntimeError('unexpected print output: '+repr(run.stdout))
            results.append({'api':api,'requests':observations,'stdout':run.stdout})
    finally:
        for server in (proxy,origin): server.shutdown();server.server_close()
        for worker in workers: worker.join()
print(json.dumps(results,separators=(',',':')))
