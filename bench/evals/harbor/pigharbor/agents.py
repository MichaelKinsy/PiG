# SPDX-License-Identifier: MIT
"""Harbor agents for the PiG benchmarks. Needs Harbor 0.24.0 importable (pigbench.harbor.HARBOR_VERSION and HARBOR_WHEEL_SHA256); see ../README.md.

PigAgent subclasses Harbor's `Pi` adapter (harbor/agents/installed/pi.py in the Harbor wheel). Harbor's adapter runs
`pi --print --mode json --session-dir <logs>/pi/sessions --provider P --model M <instruction>` and converts the session JSONL
the harness wrote into a trajectory and token accounting (`usage.input/output/cacheRead/cacheWrite/reasoning/totalTokens/cost`).
For a custom endpoint (the metering gateway) it writes models.json into the directory named by PI_CODING_AGENT_DIR. PiG reads its
own PIG_CODING_AGENT_DIR instead (PiG divergence D2) unless PIG_USE_PI_DIRS=1 selects Pi's directories, so PigAgent sets
PIG_USE_PI_DIRS=1 on every command; without it PiG fails with `Unknown provider "harbor-endpoint"`. Otherwise PiG accepts the same
flags and writes the same session and usage fields (checked by `pigbench harbor-compat`), so PigAgent puts a pinned PiG binary on
PATH under the name `pi` and the inherited command line and converter run unchanged. Nothing is patched in Harbor.

CopilotByokAgent subclasses Harbor's `CopilotCli`. Harbor's adapter forwards only a GitHub token to Copilot CLI
(copilot_cli.py, `run`), so bring-your-own-key mode has no passthrough. CopilotByokAgent adds the BYOK variables
(COPILOT_PROVIDER_BASE_URL, COPILOT_PROVIDER_TYPE, COPILOT_PROVIDER_API_KEY, COPILOT_MODEL, COPILOT_PROVIDER_MAX_PROMPT_TOKENS,
COPILOT_PROVIDER_MAX_OUTPUT_TOKENS, COPILOT_OFFLINE) from the host environment to every command the adapter runs. The key is never
written to disk or to a config file.
"""
import os
import shlex
from typing import override

from harbor.agents.installed.copilot_cli import CopilotCli
from harbor.agents.installed.pi import Pi
from harbor.environments.base import BaseEnvironment

BYOK_VARIABLES = ("COPILOT_PROVIDER_BASE_URL", "COPILOT_PROVIDER_TYPE", "COPILOT_PROVIDER_API_KEY", "COPILOT_MODEL", "COPILOT_PROVIDER_MAX_PROMPT_TOKENS",
                  "COPILOT_PROVIDER_MAX_OUTPUT_TOKENS", "COPILOT_OFFLINE")


# PiG divergence D2: PiG reads PIG_CODING_AGENT_DIR unless this selects Pi's PI_CODING_AGENT_DIR, which Harbor's Pi adapter sets.
PIG_ENV = {"PIG_USE_PI_DIRS": "1"}


def byok_env(environ=None):
    """The BYOK variables set on the host, as a dict. Missing variables are left out."""
    environ = os.environ if environ is None else environ
    return {k: environ[k] for k in BYOK_VARIABLES if environ.get(k)}


class PigAgent(Pi):
    """PiG run through Harbor's Pi adapter. Select it with `--agent-import-path pigharbor.agents:PigAgent`.

    agent kwargs: `pig_url` and `pig_sha256` (a pinned PiG release archive, checked in the container before it is unpacked) or `pig_binary`
    (a host path to a static Linux build that is uploaded). Exactly one source is required.
    """

    def __init__(self, *args, pig_url: str | None = None, pig_sha256: str | None = None, pig_binary: str | None = None, **kwargs):
        if bool(pig_url) == bool(pig_binary):
            raise ValueError("PigAgent needs exactly one of pig_url (with pig_sha256) or pig_binary")
        if pig_url and not pig_sha256:
            raise ValueError("pig_url needs pig_sha256: a PiG archive is never installed unchecked")
        super().__init__(*args, **kwargs)
        self._pig_url, self._pig_sha256, self._pig_binary = pig_url, pig_sha256, pig_binary

    @staticmethod
    @override
    def name() -> str:
        return "pig"

    @override
    def get_version_command(self) -> str | None:
        return "pig --version"

    @override
    async def install(self, environment: BaseEnvironment) -> None:
        await self.ensure_system_dependencies(environment, ("curl", "ca_certificates"))
        if self._pig_binary:
            await environment.upload_file(source_path=self._pig_binary, target_path="/tmp/pig")
            fetch = "install -m 0755 /tmp/pig /usr/local/bin/pig"
        else:
            fetch = (f"curl -fsSL -o /tmp/pig.tar.gz {shlex.quote(self._pig_url)} && echo {shlex.quote(self._pig_sha256 + '  /tmp/pig.tar.gz')} | sha256sum -c - "
                     "&& rm -rf /tmp/pig-release && mkdir -p /tmp/pig-release && tar -xzf /tmp/pig.tar.gz -C /tmp/pig-release "
                     "&& install -m 0755 \"$(find /tmp/pig-release -type f -name pig | head -1)\" /usr/local/bin/pig")
        await self.exec_as_root(environment, command=f"set -euo pipefail; {fetch} && ln -sf /usr/local/bin/pig /usr/local/bin/pi && pig --version")

    @override
    async def exec_as_agent(self, environment, command, env=None, cwd=None, timeout_sec=None):
        return await super().exec_as_agent(environment, command=command, env={**PIG_ENV, **(env or {})}, cwd=cwd, timeout_sec=timeout_sec)


class CopilotByokAgent(CopilotCli):
    """Copilot CLI in bring-your-own-key mode. Select it with `--agent-import-path pigharbor.agents:CopilotByokAgent`."""

    @staticmethod
    @override
    def name() -> str:
        return "copilot-byok"

    @override
    async def exec_as_agent(self, environment, command, env=None, cwd=None, timeout_sec=None):
        return await super().exec_as_agent(environment, command=command, env={**byok_env(), **(env or {})}, cwd=cwd, timeout_sec=timeout_sec)
