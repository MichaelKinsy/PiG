#!/usr/bin/env python3
"""Drive real RPC and emit complete records for the selected contracts.

The scenario's shared typed JSON comparator owns identity aliases. Prompt streams
remain complete in raw.json; only their recovery entry_appended records belong to
this probe's mutation contract. Session-file directories are explicit fixture inputs.
"""
import http.server
import json
import os
from pathlib import Path
import queue
import subprocess
import sys
import tempfile
import threading

REPO = Path(__file__).resolve().parents[3]


class Provider(http.server.BaseHTTPRequestHandler):
    calls = 0

    def log_message(self, *_):
        pass

    def do_POST(self):
        request = self.rfile.read(int(self.headers['Content-Length']))
        type(self).calls += 1
        if b'RETRY' in request and not self.server.retried:
            self.server.retried = True
            self.send_response(400)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(b'{"error":{"type":"overloaded_error","message":"overloaded"}}')
            return
        text = 'summary' if b'Create a structured context checkpoint summary' in request else 'hello'
        events = [
            {'type': 'message_start', 'message': {'id': 'msg_fixture', 'usage': {'input_tokens': 25, 'output_tokens': 0}}},
            {'type': 'content_block_start', 'index': 0, 'content_block': {'type': 'text', 'text': ''}},
            {'type': 'content_block_delta', 'index': 0, 'delta': {'type': 'text_delta', 'text': text}},
            {'type': 'content_block_stop', 'index': 0},
            {'type': 'message_delta', 'delta': {'stop_reason': 'end_turn'}, 'usage': {'output_tokens': 12}},
            {'type': 'message_stop'},
        ]
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        for event in events:
            self.wfile.write(('event: '+event['type']+'\ndata: '+json.dumps(event)+'\n\n').encode())


def run(host, root, initial_only=False):
    agent = root / 'agent'
    agent.mkdir()
    cwd = root / 'cwd'
    cwd.mkdir()
    server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
    server.retried = False
    worker = threading.Thread(target=server.serve_forever)
    worker.start()
    url = f'http://127.0.0.1:{server.server_port}'
    (agent / 'models.json').write_text(json.dumps({'providers': {'anthropic': {'baseUrl': url, 'api': 'anthropic-messages', 'apiKey': 'fixture-key', 'models': [{'id': 'wire-model', 'name': 'Wire model', 'reasoning': True, 'contextWindow': 200000, 'maxTokens': 8192}]}}}))
    (agent / 'settings.json').write_text(json.dumps({'defaultThinkingLevel': 'medium' if initial_only else 'off', 'compaction': {'enabled': False, 'keepRecentTokens': 1}, 'retry': {'enabled': True, 'maxRetries': 1, 'baseDelayMs': 1}}))
    binary = os.environ.get('PIG_PARITY_PIG_BIN', str(REPO / 'bin/pig')) if host == 'pig' else str(REPO / 'extensions/sdk-ts/node_modules/.bin/pi')
    env = {'PATH': os.environ['PATH'], 'HOME': str(root), 'PIG_HOME': str(root), 'PIG_CODING_AGENT_DIR': str(agent), 'PI_CODING_AGENT_DIR': str(agent), 'NO_COLOR': '1'}
    args = [binary, '--mode', 'rpc', '--session-dir', str(root / 'sessions'), '--offline', '--no-extensions', '--no-skills', '--no-context-files', '--system-prompt', 'Wire test.', '--provider', 'anthropic', '--model', 'wire-model', '--thinking', 'off', '-e', str(REPO / 'test/parity/scenarios/rpc/testdata/wire-drift-ui.mjs')]
    stderr_file = (root / 'stderr').open('w+')
    process = subprocess.Popen(args, cwd=cwd, env=env, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=stderr_file, text=True)
    records = queue.Queue()
    raw = []
    selected = []

    def reader():
        for line in process.stdout:
            value = json.loads(line)
            raw.append(value)
            records.put(value)
        records.put(None)

    reader_thread = threading.Thread(target=reader)
    reader_thread.start()

    def until(predicate):
        found = []
        while True:
            value = records.get(timeout=20)
            assert value is not None, 'RPC exited before response'
            found.append(value)
            if predicate(value):
                return found

    counter = 0

    def command(kind, **fields):
        nonlocal counter
        counter += 1
        key = f'request-{counter}'
        process.stdin.write(json.dumps(dict(type=kind, id=key, **fields))+'\n')
        process.stdin.flush()
        result = until(lambda v: v.get('type') == 'response' and v.get('id') == key)
        return result

    def exercise():
        selected.extend(command('set_thinking_level', level='high'))
        selected.extend(command('cycle_thinking_level'))
        selected.extend(command('prompt', message='/wire-ui'))
        # Keep complete recovery records, including unexpected extras. Other provider-stream events remain in raw.json.
        before = command('prompt', message='RETRY')
        prompt = before + until(lambda v: v['type'] == 'agent_settled')
        recovery = [v for v in prompt if v['type'] == 'entry_appended']
        persisted = command('get_entries')[-1]['data']['entries']
        edits = [v for v in persisted if v['type'] == 'context_edit']
        assert [v['entry'] for v in recovery] == edits and edits, (recovery, edits)
        selected.extend(recovery)
        selected.extend(command('compact'))
        selected.extend(command('fork', entryId='absent'))
        missing = root / 'explicit.jsonl'
        switched = command('switch_session', sessionPath=str(missing))
        assert switched[-1].get('data') == {'cancelled': False}, switched
        assert not missing.exists(), 'missing path must defer writes'
        selected.extend(switched)
        after = command('get_state')
        assert after[-1]['data']['sessionFile'] == str(missing)
        assert after[-1]['data']['sessionId'] != initial['sessionId']
        selected.extend(after)

    try:
        state = command('get_state')
        initial = state[-1]['data']
        selected += state
        selected += command('get_entries')
        if not initial_only:
            exercise()
        process.stdin.close()
        process.wait(timeout=20)
        reader_thread.join(timeout=20)
        assert not reader_thread.is_alive()
        assert process.returncode == 0, process.returncode
        stderr_file.seek(0)
        stderr = stderr_file.read()
        assert stderr == '', repr(stderr)
        for record in selected:
            print(json.dumps(record, separators=(',', ':')))
        print(json.dumps({'stderr': stderr, 'exitCode': process.returncode}, sort_keys=True))
    finally:
        if process.poll() is None:
            process.kill()
            process.wait()
        reader_thread.join(timeout=20)
        server.shutdown()
        server.server_close()
        worker.join()
        (root / 'raw.json').write_text(json.dumps(raw, indent=2))
        stderr_file.close()


if __name__ == '__main__':
    initial_only = '--initial' in sys.argv
    if initial_only:
        sys.argv.remove('--initial')
    if len(sys.argv) > 2:
        root = Path(sys.argv[2])
        root.mkdir()
        run(sys.argv[1], root, initial_only)
    else:
        with tempfile.TemporaryDirectory(prefix='rpc-wire-') as tmp:
            run(sys.argv[1], Path(tmp), initial_only)
