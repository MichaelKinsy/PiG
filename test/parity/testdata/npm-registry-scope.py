#!/usr/bin/env python3
"""Measure real package-update metadata traffic against two loopback registries."""
import http.server
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import threading


def main():
    binary, scope = sys.argv[1:]
    node = subprocess.check_output(["node", "-p", "process.execPath"], text=True).strip()
    npm = str(Path(node).parent / "npm")
    if binary == "pig":
        command = [os.environ["PIG_PARITY_PIG_BIN"]]
    else:
        command = [node, str(Path(os.environ["PI_PACKAGE_DIR"]) / "dist/bundle/cli.js")]
    requests = []

    class Registry(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass

        def do_GET(self):
            requests.append(self.path)
            version = {"name": "fake-package", "version": "1.0.0"}
            data = json.dumps({"name": "fake-package", "dist-tags": {"latest": "1.0.0"}, "versions": {"1.0.0": version}}).encode()
            self.send_response(200)
            self.send_header("Content-Length", str(len(data)))
            self.end_headers()
            self.wfile.write(data)

    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Registry)
    thread = threading.Thread(target=server.serve_forever)
    thread.start()
    try:
        with tempfile.TemporaryDirectory(dir=os.environ["SCOPE_PROBE_TEMP"]) as tmp:
            root = Path(tmp)
            project, agent = root / "project", root / "agent"
            config = project / (".pig" if binary == "pig" else ".pi")
            config.mkdir(parents=True)
            agent.mkdir()
            install_root = agent / "npm" if scope == "user" else config / "npm"
            installed = install_root / "node_modules" / "fake-package"
            installed.mkdir(parents=True)
            (installed / "package.json").write_text('{"name":"fake-package","version":"1.0.0"}')
            (install_root / "package.json").write_text('{"private":true,"dependencies":{"fake-package":"1.0.0"}}')
            userconfig = root / "user.npmrc"
            userconfig.write_text(f"registry=http://127.0.0.1:{server.server_port}/user/\nfund=false\naudit=false\nupdate-notifier=false\n")
            (project / ".npmrc").write_text(f"registry=http://127.0.0.1:{server.server_port}/project/\n")
            record = root / "commands.jsonl"
            wrapper = root / "selected-command.cjs"
            wrapper.write_text("""const fs=require('node:fs');
const [record, npm, ...args]=process.argv.slice(2);
fs.appendFileSync(record, JSON.stringify({cwd:process.cwd(),args})+'\\n');
const result=require('node:child_process').spawnSync(npm,args,{stdio:'inherit'});
if(result.error) throw result.error;
process.exit(result.status === null ? 1 : result.status);
""")
            settings = {"npmCommand": [node, str(wrapper), str(record), npm, "--userconfig", str(userconfig)]}
            if scope == "user":
                settings["packages"] = ["npm:fake-package"]
            (agent / "settings.json").write_text(json.dumps(settings))
            (config / "settings.json").write_text(json.dumps({"packages": ["npm:fake-package"]} if scope == "project" else {}))
            (agent / "trust.json").write_text(json.dumps({str(project): scope == "project"}))
            env = {k: v for k, v in os.environ.items() if not k.lower().startswith(("npm_", "http_proxy", "https_proxy", "all_proxy", "pi_", "pig_"))}
            env.update({"HOME": str(root), "PIG_HOME": str(root / "home"), "PI_CODING_AGENT_DIR": str(agent), "PIG_CODING_AGENT_DIR": str(agent), "PI_SKIP_VERSION_CHECK": "1", "NPM_CONFIG_CACHE": str(root / "cache"), "PATH": str(Path(node).parent) + os.pathsep + os.environ["PATH"]})
            result = subprocess.run(command + ["update", "npm:fake-package"], cwd=project, env=env, text=True, capture_output=True, timeout=30)
            if result.returncode != 0:
                raise AssertionError((result.returncode, result.stdout, result.stderr))
            calls = [json.loads(line) for line in record.read_text().splitlines()]
            # One current package requires one lookup and no install. An unconditional reinstall cannot pass this probe.
            expected_args = ["--userconfig", str(userconfig), "view", "fake-package", "version", "--json"]
            if len(calls) != 1 or calls[0]["args"] != expected_args:
                raise AssertionError(f"expected selected command's metadata lookup only, got {calls}; output={result.stdout!r} stderr={result.stderr!r}")
            lookup_dir = Path(calls[0]["cwd"])
            lookup = "project" if lookup_dir == project else "user" if lookup_dir == agent / "npm" else str(lookup_dir)
            package_requests = [path for path in requests if path.endswith("/fake-package")]
            if len(package_requests) != 1:
                raise AssertionError(f"expected one package lookup, got {requests}")
            registry = package_requests[0].split("/")[1]
            print(f"metadata cwd={lookup}; registry={registry}; selected-command=yes; installs=0")
    finally:
        server.shutdown()
        thread.join()
        server.server_close()


if __name__ == "__main__":
    main()
