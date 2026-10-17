# SPDX-License-Identifier: MIT
import http.client
import json
import os
import subprocess
import tempfile
import textwrap
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer

from pigbench import gateway, harbor, lock


def trial(arm, task, reward, cost=1.0, seconds=10.0, exc=None):
    r = {"task_name": task, "agent_info": {"name": "x"}, "config": {"agent": {"env": {"PIGBENCH_ARM": arm}}},
         "agent_result": {"n_input_tokens": 100, "n_output_tokens": 10, "cost_usd": cost},
         "agent_execution": {"started_at": "2026-10-10T00:00:00Z", "finished_at": f"2026-10-10T00:00:{int(seconds):02d}Z"}}
    if exc:
        r["exception_info"] = {"exception_type": exc}
    else:
        r["verifier_result"] = {"rewards": {"reward": reward}}
    return harbor.trial_row(r)


class AnalysisTest(unittest.TestCase):
    def rows(self, pairs):
        rows = []
        for arm, results in pairs.items():
            for task, rewards in results.items():
                rows += [trial(arm, task, r) for r in rewards]
        return rows

    def test_identical_arms_are_equivalent_and_not_different(self):
        tasks = {f"t{i}": [1, 0, 1] if i % 2 else [0, 0, 1] for i in range(30)}
        rep = harbor.analyze(self.rows({"pi": tasks, "pig": tasks}), "pi", 0.05, 1)
        e = rep["arms"]["pig"]["vs_reference"]
        self.assertEqual(e["mean_diff"], 0)
        self.assertEqual(e["sign_flip_p"], 1.0)
        self.assertTrue(e["tost"]["equivalent"])

    def test_a_uniform_gain_is_a_difference_not_parity(self):
        base = {f"t{i}": [0, 0, 0] for i in range(20)}
        better = {f"t{i}": [1, 1, 1] for i in range(20)}
        rep = harbor.analyze(self.rows({"pi": base, "pig": better}), "pi", 0.05, 1)
        e = rep["arms"]["pig"]["vs_reference"]
        self.assertAlmostEqual(e["mean_diff"], 1.0)
        self.assertLess(e["sign_flip_p"], 0.001)
        self.assertFalse(e["tost"]["equivalent"])
        self.assertEqual(rep["arms"]["pig"]["pass_rate"], 1.0)

    def test_infrastructure_errors_are_counted_and_excluded_from_scores(self):
        rows = [trial("pi", f"t{i}", 1) for i in range(8)] + [trial("pi", "t8", None, exc="EnvironmentStartTimeoutError"), trial("pi", "t9", None, exc="DockerError")]
        rep = harbor.analyze(rows, "pi", 0.05, 1)
        a = rep["arms"]["pi"]
        self.assertEqual((a["trials"], a["infra_errors"], a["scored"]), (10, 2, 8))
        self.assertAlmostEqual(a["infra_rate"], 0.2)
        self.assertEqual(a["pass_rate"], 1.0)

    def test_resampling_is_by_task_and_pairing_drops_unshared_tasks(self):
        rows = self.rows({"pi": {"t1": [1, 1], "t2": [0, 0]}, "pig": {"t1": [1, 1], "t3": [1, 1]}})
        rep = harbor.analyze(rows, "pi", 0.05, 1)
        self.assertEqual(rep["arms"]["pig"]["vs_reference"]["tasks"], 1)

    def test_cost_ratio_of_totals(self):
        rows = [trial("pi", f"t{i}", 1, cost=1.0) for i in range(5)] + [trial("pig", f"t{i}", 1, cost=2.0) for i in range(5)]
        rep = harbor.analyze(rows, "pi", 0.05, 1)
        self.assertAlmostEqual(rep["arms"]["pig"]["vs_reference"]["ratio_cost_usd"]["ratio_of_totals"], 2.0)

    def test_aa_floor_of_identical_runs_is_zero(self):
        tasks = {f"t{i}": [1, 0] for i in range(12)}
        rep = harbor.analyze(self.rows({"pi-a": tasks, "pi-b": tasks}), "pi-a", 0.05, 1, ("pi-a", "pi-b"))
        self.assertEqual(rep["aa"]["mean"], 0)

    def test_non_trial_json_is_ignored(self):
        self.assertIsNone(harbor.trial_row({"id": "job", "stats": {}}))


class PlanTest(unittest.TestCase):
    def test_plan_pairs_and_no_key_in_any_config(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = harbor.plan(tmp, "claude-x", "gpt-y", "/bin/pig")
            self.assertEqual(len(paths), 6)
            names = {os.path.basename(p) for p in paths}
            self.assertIn("swebench-pro-claude.json", names)
            self.assertIn("terminal-bench-2.1-aa-pi-vs-pi.json", names)
            for p in paths:
                text = open(p).read()
                self.assertNotIn("sk-", text, p)
                cfg = json.loads(text)
                for agent in cfg["agents"]:
                    keys = {k: v for k, v in agent["env"].items() if "KEY" in k or "TOKEN" in k}
                    # Every key variable holds the placeholder the gateway swaps, and the provider's own is set: no real key in a config or a task container.
                    self.assertEqual(set(keys.values()), {harbor.GATEWAY_PLACEHOLDER_KEY}, (p, agent["env"]))
                    self.assertIn(harbor.PAIRS["claude" if "claude" in os.path.basename(p) or "aa" in os.path.basename(p) else "gpt"]["key_env"], keys, p)
                self.assertEqual(cfg["agents"][0]["extra_allowed_hosts"], [harbor.GATEWAY_HOST])  # the only host an agent may reach
                self.assertEqual(cfg["n_attempts"], 3 if "swebench" in p else 5)
            claude = json.load(open(os.path.join(tmp, "swebench-pro-claude.json")))
            self.assertEqual([a.get("name") or a["import_path"] for a in claude["agents"]],
                             ["pigharbor.agents:PigAgent", "pigharbor.agents:PigAgent", "pi", "claude-code", "pigharbor.agents:CopilotByokAgent"])
            self.assertTrue(all(a["model_name"] == "anthropic/claude-x" for a in claude["agents"]))  # one snapshot for every arm of the pair
            versions = {a["kwargs"].get("version") for a in claude["agents"] if a.get("name") in ("pi", "claude-code")}
            self.assertEqual(versions, {lock.read()["arms"]["pi-1.1.0"]["version"], lock.read()["arms"]["claude"]["version"]})
            aa = json.load(open(os.path.join(tmp, "swebench-pro-aa-pi-vs-pi.json")))
            self.assertEqual([a["env"]["PIGBENCH_ARM"] for a in aa["agents"]], ["pi-a", "pi-b"])
            self.assertEqual(aa["agents"][0]["kwargs"], aa["agents"][1]["kwargs"])  # identical configuration: only the label differs

    @unittest.skipUnless(os.environ.get("HARBOR_PYTHON"), "needs HARBOR_PYTHON")
    def test_pig_agent_selects_pis_directories_on_every_command(self):
        # D2: PiG ignores the PI_CODING_AGENT_DIR that Harbor's Pi adapter sets unless PIG_USE_PI_DIRS=1; harbor-compat runs PiG with harbor.PIG_ENV.
        code = textwrap.dedent("""
            import asyncio, json, pathlib, sys, tempfile
            from harbor.agents.installed.base import BaseInstalledAgent
            from pigharbor import agents
            seen = []
            async def fake(self, environment, command, env=None, cwd=None, timeout_sec=None):
                seen.append(env)
            BaseInstalledAgent.exec_as_agent = fake
            agent = agents.PigAgent(logs_dir=pathlib.Path(tempfile.mkdtemp()), model_name="a/b", pig_binary="/x")
            asyncio.run(agent.exec_as_agent(None, "pi --print x", env={"ANTHROPIC_API_KEY": "k"}))
            print(json.dumps({"seen": seen, "pig_env": agents.PIG_ENV}))
        """)
        done = subprocess.run([os.environ["HARBOR_PYTHON"], "-c", code], capture_output=True, text=True, env=dict(os.environ, PYTHONPATH=os.path.join(lock.EVALS, "harbor")), timeout=120)
        self.assertEqual(done.returncode, 0, done.stderr)
        got = json.loads(done.stdout.splitlines()[-1])
        self.assertEqual(got["seen"], [{"PIG_USE_PI_DIRS": "1", "ANTHROPIC_API_KEY": "k"}])
        self.assertEqual(got["pig_env"], harbor.PIG_ENV)

    @unittest.skipUnless(os.environ.get("HARBOR_PYTHON"), "needs HARBOR_PYTHON")
    def test_harbors_pi_adapter_routes_pi_and_pig_through_the_gateway_with_the_placeholder_key(self):
        # Harbor's Pi adapter writes models.json for a custom endpoint only with an API-key variable; with none it raises, and a real key on the
        # host would otherwise be resolved and passed into the container. The host key below must not reach the agent.
        code = textwrap.dedent("""
            import json, pathlib, sys, tempfile
            from harbor.agents.installed.pi import Pi
            from pigharbor.agents import PigAgent
            for cfg in json.loads(sys.argv[1]):
                kwargs = dict(cfg["kwargs"])
                cls = PigAgent if cfg.get("import_path") else Pi
                agent = cls(logs_dir=pathlib.Path(tempfile.mkdtemp()), model_name=cfg["model_name"], extra_env=cfg["env"], **kwargs)
                access = agent.model_connection
                models = agent._build_custom_models_json(access, cfg["model_name"].split("/", 1)[1])
                provider = models["providers"]["harbor-endpoint"]
                print(json.dumps({"base": provider["baseUrl"], "apiKey": provider["apiKey"], "api": provider["api"], "key": access.api_key, "env": access.env}))
        """)
        for pair, base_env, key_env in (("claude", "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY"), ("gpt", "OPENAI_BASE_URL", "OPENAI_API_KEY")):
            cfgs = [harbor.agent_config(a, pair, "m-1", "/bin/pig") for a in ("pi-1.1.0", "pig-0.5", "pig-0.4.1")]
            env = dict(os.environ, PYTHONPATH=os.path.join(lock.EVALS, "harbor"), **{key_env: "real-host-key"})
            done = subprocess.run([os.environ["HARBOR_PYTHON"], "-c", code, json.dumps(cfgs)], capture_output=True, text=True, env=env, timeout=120)
            self.assertEqual(done.returncode, 0, done.stderr)
            out = [json.loads(line) for line in done.stdout.splitlines()]
            self.assertEqual(len(out), 3)
            for cfg, got in zip(cfgs, out):
                self.assertEqual(got["base"], cfg["env"][base_env])
                self.assertEqual(got["apiKey"], "$" + key_env)
                self.assertEqual(got["api"], harbor.PAIRS[pair]["model_api"])
                self.assertEqual(got["key"], harbor.GATEWAY_PLACEHOLDER_KEY)
                self.assertNotIn("real-host-key", json.dumps(got))

    @unittest.skipUnless(os.environ.get("HARBOR_PYTHON"), "needs HARBOR_PYTHON")
    def test_pig_agent_install_runs_against_harbors_dependency_table(self):
        # Harbor 0.24.0 names the package ca_certificates; an unknown name raises before anything is installed.
        code = textwrap.dedent("""
            import asyncio, json, pathlib, tempfile
            from pigharbor.agents import PigAgent
            class Env:
                def __init__(self): self.calls = []
                async def exec(self, command, user=None, **kw):
                    self.calls.append(["exec", str(user), command])
                    class R: return_code = 0; stdout = "apt-get"; stderr = ""
                    return R()
                async def upload_file(self, source_path, target_path): self.calls.append(["upload", str(source_path), target_path])
            out = []
            for kw in ({"pig_binary": "/bin/true"}, {"pig_url": "https://example.invalid/pig.tar.gz", "pig_sha256": "ab" * 32}):
                env = Env()
                asyncio.run(PigAgent(logs_dir=pathlib.Path(tempfile.mkdtemp()), model_name="a/b", **kw).install(env))
                out.append(env.calls)
            print(json.dumps(out))
        """)
        done = subprocess.run([os.environ["HARBOR_PYTHON"], "-c", code], capture_output=True, text=True, env=dict(os.environ, PYTHONPATH=os.path.join(lock.EVALS, "harbor")), timeout=120)
        self.assertEqual(done.returncode, 0, done.stderr)
        binary, archive = json.loads(done.stdout.splitlines()[-1])
        self.assertIn(["upload", "/bin/true", "/tmp/pig"], binary)
        final = binary[-1][2]
        self.assertIn("install -m 0755 /tmp/pig /usr/local/bin/pig", final)
        self.assertIn("ln -sf /usr/local/bin/pig /usr/local/bin/pi", final)
        fetch = archive[-1][2]
        self.assertIn("sha256sum -c -", fetch)
        self.assertLess(fetch.index("sha256sum -c -"), fetch.index("tar -xzf"))  # checked before it is unpacked
        self.assertIn("-C /tmp/pig-release", fetch)  # its own directory: no stray /tmp/pig can be installed instead

    @unittest.skipUnless(os.environ.get("HARBOR_PYTHON"), "needs HARBOR_PYTHON")
    def test_configs_validate_against_harbors_schema_and_agents_construct(self):
        with tempfile.TemporaryDirectory() as tmp:
            paths = harbor.plan(tmp, "claude-x", "gpt-y", "/bin/pig")
            ok, message = harbor.validate(paths)
            self.assertTrue(ok, message)
            code = ("import pathlib,tempfile\nfrom pigharbor.agents import PigAgent, CopilotByokAgent, byok_env\nl=pathlib.Path(tempfile.mkdtemp())\n"
                    "assert PigAgent(logs_dir=l, model_name='a/b', pig_binary='/x').name()=='pig'\n"
                    "try:\n    PigAgent(logs_dir=l, model_name='a/b', pig_url='u')\n    raise SystemExit('unchecked archive accepted')\nexcept ValueError: pass\n"
                    "assert byok_env({'COPILOT_MODEL':'m','HOME':'/h'})=={'COPILOT_MODEL':'m'}\n")
            done = subprocess.run([os.environ["HARBOR_PYTHON"], "-c", code], capture_output=True, text=True, env=dict(os.environ, PYTHONPATH=os.path.join(lock.EVALS, "harbor")), timeout=120)
            self.assertEqual(done.returncode, 0, done.stderr)


class GatewayTest(unittest.TestCase):
    def test_usage_extraction_across_protocols(self):
        self.assertEqual(gateway.find_usage(b'{"usage":{"prompt_tokens":5,"completion_tokens":2}}'), {"prompt_tokens": 5, "completion_tokens": 2})
        sse = b'data: {"type":"message_start","message":{"usage":{"input_tokens":9,"output_tokens":1}}}\n\ndata: {"type":"message_delta","usage":{"output_tokens":7}}\n\n'
        self.assertEqual(gateway.find_usage(sse), {"input_tokens": 9, "output_tokens": 7})
        self.assertEqual(gateway.find_usage(b'data: {"response":{"usage":{"input_tokens":3}}}\n\n'), {"input_tokens": 3})
        self.assertEqual(gateway.find_usage(b"not json"), {})

    def test_chunked_request_bodies_and_gzip_replies(self):
        import gzip
        import io
        seen = {}
        reply = json.dumps({"usage": {"input_tokens": 21, "output_tokens": 3}}).encode()

        class Upstream(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                seen["body"] = self.rfile.read(int(self.headers["Content-Length"]))
                seen["x-api-key"] = self.headers.get_all("x-api-key")
                data = gzip.compress(reply)
                self.send_response(200)
                self.send_header("Content-Encoding", "gzip")
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        up = HTTPServer(("127.0.0.1", 0), Upstream)
        threading.Thread(target=up.serve_forever, daemon=True).start()
        with tempfile.TemporaryDirectory() as tmp:
            log = os.path.join(tmp, "g.jsonl")
            gw = gateway.Gateway(("127.0.0.1", 0), f"http://127.0.0.1:{up.server_address[1]}", "REAL-KEY", log, "x-api-key")
            threading.Thread(target=gw.serve_forever, daemon=True).start()
            payload = json.dumps({"model": "m", "messages": [{"role": "user", "content": "x" * 3000}]}).encode()
            conn = http.client.HTTPConnection("127.0.0.1", gw.server_address[1], timeout=10)
            conn.request("POST", "/codex/v1/messages", body=iter([payload[:1000], payload[1000:]]), headers={"x-api-key": "dummy", "Transfer-Encoding": "chunked"}, encode_chunked=True)
            resp = conn.getresponse()
            raw = resp.read()
            conn.close()
            gw.shutdown()
            up.shutdown()
            with open(log) as f:
                entry = json.loads(f.readline())
        self.assertEqual(seen["body"], payload)  # the streamed body reaches the upstream whole
        self.assertEqual(seen["x-api-key"], ["REAL-KEY"])  # the client's dummy key is replaced, not sent alongside
        self.assertEqual(gzip.GzipFile(fileobj=io.BytesIO(raw)).read(), reply)  # the client still gets the original encoded bytes
        self.assertEqual(entry["usage"], {"input_tokens": 21, "output_tokens": 3})
        self.assertEqual(entry["request_bytes"], len(payload))
        self.assertEqual(gateway.decoded(b"x", "br"), None)

    def test_gateway_forwards_swaps_the_key_and_logs_usage_per_arm(self):
        seen = {}

        class Upstream(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                seen["auth"] = self.headers.get("Authorization")
                seen["path"] = self.path
                seen["body"] = self.rfile.read(int(self.headers["Content-Length"]))
                data = json.dumps({"usage": {"prompt_tokens": 11, "completion_tokens": 4}, "ok": True}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        up = HTTPServer(("127.0.0.1", 0), Upstream)
        threading.Thread(target=up.serve_forever, daemon=True).start()
        with tempfile.TemporaryDirectory() as tmp:
            log = os.path.join(tmp, "g.jsonl")
            gw = gateway.Gateway(("127.0.0.1", 0), f"http://127.0.0.1:{up.server_address[1]}/api", "REAL-KEY", log)
            threading.Thread(target=gw.serve_forever, daemon=True).start()
            conn = http.client.HTTPConnection("127.0.0.1", gw.server_address[1], timeout=10)
            conn.request("POST", "/pig-0.5/v1/chat/completions", json.dumps({"x": 1}), {"Authorization": "Bearer dummy", "Content-Type": "application/json"})
            resp = conn.getresponse()
            body = json.loads(resp.read())
            conn.close()
            gw.shutdown()
            up.shutdown()
            entries = [json.loads(line) for line in open(log)]
        self.assertTrue(body["ok"])
        self.assertEqual(seen["auth"], "Bearer REAL-KEY")
        self.assertEqual(seen["path"], "/api/v1/chat/completions")  # arm prefix stripped, upstream path prefix kept
        self.assertEqual(json.loads(seen["body"]), {"x": 1})
        self.assertEqual((entries[0]["arm"], entries[0]["status"], entries[0]["usage"]), ("pig-0.5", 200, {"prompt_tokens": 11, "completion_tokens": 4}))
        self.assertNotIn("REAL-KEY", json.dumps(entries))

    def test_every_accepted_connection_has_nagle_off(self):
        # The gateway forwards a stream in many small writes; with Nagle on, each write after the first waits for the
        # client's delayed ACK (up to 40 ms on Linux) on a reused connection. The accepted socket itself is checked.
        import socket

        class Upstream(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *_):
                pass

            def do_POST(self):
                self.rfile.read(int(self.headers["Content-Length"]))
                data = b'{"ok":true}'
                self.send_response(200)
                self.send_header("Content-Length", str(len(data)))
                self.end_headers()
                self.wfile.write(data)

        up = HTTPServer(("127.0.0.1", 0), Upstream)
        threading.Thread(target=up.serve_forever, daemon=True).start()
        with tempfile.TemporaryDirectory() as tmp:
            gw = gateway.Gateway(("127.0.0.1", 0), f"http://127.0.0.1:{up.server_address[1]}", "REAL-KEY", os.path.join(tmp, "g.jsonl"))
            nodelay, done = [], threading.Event()
            served = gw.finish_request

            def finish_request(request, address):
                served(request, address)
                nodelay.append(request.getsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY))
                done.set()

            gw.finish_request = finish_request
            threading.Thread(target=gw.serve_forever, daemon=True).start()
            conn = http.client.HTTPConnection("127.0.0.1", gw.server_address[1], timeout=10)
            for _ in range(2):
                conn.request("POST", "/pig/v1/chat/completions", "{}", {"Content-Type": "application/json"})
                self.assertEqual(json.loads(conn.getresponse().read()), {"ok": True})
            conn.close()
            self.assertTrue(done.wait(10))
            gw.shutdown()
            up.shutdown()
        self.assertEqual(len(nodelay), 1)  # both requests reused one connection
        self.assertTrue(nodelay[0])


if __name__ == "__main__":
    unittest.main()
