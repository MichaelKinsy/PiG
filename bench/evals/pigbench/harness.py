# SPDX-License-Identifier: MIT
"""Isolated runtime for one arm: HOME, config directories, a dummy key, the mock endpoint, and argv builders.

No-spend benches never give a harness a live key or a vendor login. Every arm gets a dummy key value and a HOME that
holds no credentials. A CLI that refuses to run without a real login is excluded from the bench and the report states
the exact error (see overhead.Limitation).
"""
import json
import re
import os
import shutil

from .mock import MODEL

CREDENTIAL_PREFIXES = ("OPENAI_", "ANTHROPIC_", "GEMINI_", "GOOGLE_", "AZURE_", "AWS_", "PI_", "PIG_", "CODEX_", "CLAUDE_", "OPENCODE_", "COPILOT_", "GH_", "GITHUB_", "BENCH_")
DUMMY_KEY = "bench-dummy-key-not-a-credential"
# With a custom model provider Codex 0.162 still contacts chatgpt.com (cloud config, plugin catalog) and github.com (plugin
# marketplace) at startup. A no-spend bench turns those features off; with them on, the egress audit fails the run.
CODEX_NO_EGRESS = """[features]
plugins = false
recommended_plugins = false
remote_models = false
plugin_sharing = false
apps = false
remote_plugin = false

[analytics]
enabled = false

[otel]
exporter = "none"

[feedback]
enabled = false

[history]
persistence = "none"

[tui]
show_tooltips = false
"""


class Harness:
    """One arm prepared against one mock endpoint, with its own HOME and project directory."""

    def __init__(self, arm, work, server, context_window=128000, compaction=None, retry_base_ms=50, extensions=(), tag="", settings_extra=None):
        self.arm, self.server, self.base_url = arm, server, server.base_url
        name = arm.id + (f"-{tag}" if tag else "")
        self.home = os.path.join(work, name, "home")
        self.cwd = os.path.join(work, name, "project")
        self.sessions = os.path.join(work, name, "sessions")
        self.work = work
        for d in (self.home, self.cwd, self.sessions, os.path.join(self.home, "tmp")):
            os.makedirs(d, exist_ok=True)
        self.agent_dir = os.path.join(self.home, ".agent")
        self.extensions = list(extensions)
        self.settings_extra = dict(settings_extra or {})
        env = {k: v for k, v in os.environ.items() if not k.startswith(CREDENTIAL_PREFIXES) and not k.endswith(("_API_KEY", "_TOKEN", "_SECRET"))}
        for k in ("HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"):
            env.pop(k, None)
        env.update({"HOME": self.home, "XDG_CONFIG_HOME": os.path.join(self.home, ".config"), "XDG_DATA_HOME": os.path.join(self.home, ".local", "share"),
                    "XDG_CACHE_HOME": os.path.join(self.home, ".cache"), "XDG_STATE_HOME": os.path.join(self.home, ".local", "state"),
                    "NO_COLOR": "1", "TERM": "xterm-256color", "NO_PROXY": "127.0.0.1,localhost,::1", "no_proxy": "127.0.0.1,localhost,::1"})
        family = arm.family
        if family in ("pig", "pi"):
            env.update({"PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0", "PIG_OFFLINE": "1"})
            if family == "pig":
                env["PIG_HOME"] = os.path.join(self.home, ".pig")
                env["PIG_CODING_AGENT_DIR"] = self.agent_dir
            else:
                env[arm.spec.get("agent_dir_env", "PI_CODING_AGENT_DIR")] = self.agent_dir
            env.update(arm.spec.get("env", {}))
            self._write_pi_config(context_window, compaction, retry_base_ms)
        elif family == "claude":
            env.update({"ANTHROPIC_BASE_URL": server.origin, "ANTHROPIC_API_KEY": DUMMY_KEY, "CLAUDE_CONFIG_DIR": os.path.join(self.home, ".claude"),
                        "DISABLE_TELEMETRY": "1", "DISABLE_AUTOUPDATER": "1", "DISABLE_UPDATES": "1", "CLAUDE_CODE_TMPDIR": os.path.join(self.home, "tmp"), "DISABLE_ERROR_REPORTING": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1",
                        "ANTHROPIC_DEFAULT_HAIKU_MODEL": MODEL, "ANTHROPIC_SMALL_FAST_MODEL": MODEL, "CLAUDE_CODE_MAX_CONTEXT_TOKENS": str(context_window)})
            self._write_claude_config()
        elif family == "codex":
            env.update({"CODEX_HOME": os.path.join(self.home, ".codex"), "BENCH_API_KEY": DUMMY_KEY})
            os.makedirs(env["CODEX_HOME"], exist_ok=True)
            with open(os.path.join(env["CODEX_HOME"], "config.toml"), "w") as f:
                f.write(f'model = "{MODEL}"\nmodel_provider = "bench"\nmodel_context_window = {context_window}\ncheck_for_update_on_startup = false\ncli_auth_credentials_store = "file"\n\n{CODEX_NO_EGRESS}\n[projects."{self.cwd}"]\ntrust_level = "trusted"\n\n[model_providers.bench]\nname = "bench"\nbase_url = "{self.base_url}"\n'
                        'env_key = "BENCH_API_KEY"\nwire_api = "responses"\n')
        elif family == "omp":
            # oh-my-pi reads models.yml and config.yml (JSON is YAML) from the agent directory; without setupVersion 2 and the wizard off it waits at a first-run wizard.
            env.update({"PI_CODING_AGENT_DIR": self.agent_dir, "PI_OFFLINE": "1", "PI_SKIP_VERSION_CHECK": "1", "PI_TELEMETRY": "0"})
            os.makedirs(self.agent_dir, exist_ok=True)
            with open(os.path.join(self.agent_dir, "models.yml"), "w") as f:
                json.dump({"providers": {"bench": {"baseUrl": self.base_url, "api": "openai-completions", "apiKey": DUMMY_KEY,
                                                   "models": [{"id": MODEL, "name": "Bench 1", "contextWindow": context_window, "maxTokens": 4096}]}}}, f)
            with open(os.path.join(self.agent_dir, "config.yml"), "w") as f:
                json.dump({"setupVersion": 2, "startup": {"setupWizard": False}}, f)
        elif family == "opencode":
            # The provider is passed as inline configuration; without doom_loop allowed a repeated identical tool call stops at a permission prompt.
            env.update({"OPENCODE_DISABLE_AUTOUPDATE": "1", "OPENCODE_DISABLE_MODELS_FETCH": "1", "OPENCODE_CONFIG_CONTENT": json.dumps({
                "provider": {"bench": {"npm": "@ai-sdk/openai-compatible", "name": "bench", "options": {"baseURL": self.base_url, "apiKey": DUMMY_KEY},
                                       "models": {MODEL: {"name": "Bench 1", "limit": {"context": context_window, "output": 4096}}}}},
                "permission": {"edit": "allow", "bash": "allow", "webfetch": "allow", "doom_loop": "allow", "external_directory": "allow"}, "share": "disabled", "autoupdate": False})})
        elif family == "copilot":
            os.makedirs(os.path.join(self.home, ".copilot"), exist_ok=True)
            with open(os.path.join(self.home, ".copilot", "config.json"), "w") as f:
                json.dump({"trusted_folders": [self.cwd], "banner": "never", "check_for_updates": False}, f)
            env.update({"COPILOT_PROVIDER_BASE_URL": self.base_url, "COPILOT_PROVIDER_TYPE": "openai", "COPILOT_PROVIDER_API_KEY": DUMMY_KEY, "COPILOT_MODEL": MODEL,
                        "COPILOT_PROVIDER_MAX_PROMPT_TOKENS": str(context_window), "COPILOT_PROVIDER_MAX_OUTPUT_TOKENS": "4096", "COPILOT_OFFLINE": "true",
                        "COPILOT_AUTO_UPDATE": "false", "COPILOT_HOME": os.path.join(self.home, ".copilot")})
        self.env = env

    def isolation_env(self):
        """The variables this harness sets beyond the inherited environment, with the throwaway root masked: part of the configuration under comparison."""
        out = {}
        for k, v in sorted(self.env.items()):
            if os.environ.get(k) != v:
                out[k] = "<dummy key>" if v == DUMMY_KEY else re.sub(r"(127\.0\.0\.1):\d+", r"\1:<port>", v.replace(self.work, "<work>"))
        dropped = sorted(k for k in os.environ if k not in self.env and (k.startswith(CREDENTIAL_PREFIXES) or k.endswith(("_API_KEY", "_TOKEN", "_SECRET")) or "PROXY" in k.upper()))
        return {"set": out, "scrubbed": dropped}

    def _write_pi_config(self, context_window, compaction, retry_base_ms):
        os.makedirs(self.agent_dir, exist_ok=True)
        with open(os.path.join(self.agent_dir, "models.json"), "w") as f:
            json.dump({"providers": {"bench": {"baseUrl": self.base_url, "api": "openai-completions", "apiKey": DUMMY_KEY,
                                               "models": [{"id": MODEL, "name": "Bench 1", "contextWindow": context_window, "maxTokens": 4096}]}}}, f)
        settings = {"defaultProvider": "bench", "defaultModel": MODEL, "retry": {"enabled": True, "maxRetries": 3, "baseDelayMs": retry_base_ms},
                    "quietStartup": True, "collapseChangelog": True}
        if compaction:
            settings["compaction"] = compaction
        settings.update(self.settings_extra)
        with open(os.path.join(self.agent_dir, "settings.json"), "w") as f:
            json.dump(settings, f)

    def _write_claude_config(self):
        """Skip first-run dialogs that would block an interactive run: onboarding, the trust prompt, the API-key approval."""
        config_dir = os.path.join(self.home, ".claude")
        os.makedirs(config_dir, exist_ok=True)
        state = {"hasCompletedOnboarding": True, "customApiKeyResponses": {"approved": [DUMMY_KEY[-20:]], "rejected": []},
                 "projects": {self.cwd: {"hasTrustDialogAccepted": True, "allowedTools": []}}, "theme": "dark"}
        for path in (os.path.join(config_dir, ".claude.json"), os.path.join(self.home, ".claude.json")):
            with open(path, "w") as f:
                json.dump(state, f)

    @property
    def native_pi(self):
        return self.arm.family in ("pig", "pi")

    def _common(self):
        if not self.native_pi:
            return []
        args = ["--provider", "bench", "--model", MODEL]
        for path in self.extensions:
            args += ["-e", path]
        return args

    def print_argv(self, prompt, session=False):
        family, b = self.arm.family, self.arm.binary
        if family == "claude":
            return [b, "-p", prompt, "--bare", "--model", MODEL, "--permission-mode", "bypassPermissions"]
        if family == "codex":
            return [b, "exec", "-m", MODEL, "--skip-git-repo-check", "--sandbox", "danger-full-access", prompt]
        if family == "copilot":
            return [b, "-p", prompt, "--allow-all-tools", "--no-custom-instructions"]
        if family == "omp":
            return [b, "--provider", "bench", "--model", MODEL, "--no-session", "-p", prompt]
        if family == "opencode":
            return [b, "run", "--model", f"bench/{MODEL}", prompt]
        return [b, *self._common(), *([] if session else ["--no-session"]), *(["--session-dir", self.sessions] if session else []), "-p", prompt]

    def rpc_argv(self, session=True):
        return [self.arm.binary, "--mode", "rpc", *self._common(), *(["--session-dir", self.sessions] if session else ["--no-session"])]

    def tui_argv(self):
        family, b = self.arm.family, self.arm.binary
        if family == "claude":
            return [b, "--model", MODEL]
        if family == "codex":
            return [b, "--sandbox", "danger-full-access"]
        if family == "copilot":
            return [b, "--allow-all-tools"]
        return [b, *self._common(), "--no-session"]

    def supports(self, scenario):
        if self.native_pi:
            return True
        return scenario in ("version", "print", "print-tools", "pty")

    def cleanup(self):
        shutil.rmtree(os.path.dirname(self.home), ignore_errors=True)
