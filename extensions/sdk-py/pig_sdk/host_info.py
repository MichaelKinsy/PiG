"""Pi's ``getAgentDir()``, ``CONFIG_DIR_NAME`` and ``VERSION`` (config.ts) as the PiG host resolves them.

They are callable while an extension's factory runs, before the host's ready message, so the host starts every extension process with them in
``PIG_EXT_HOST_INFO``.
"""
import json
import os
from typing import Any, Dict

_ENV = "PIG_EXT_HOST_INFO"


class HostInfoUnavailable(RuntimeError):
    """The process was not started by a PiG host that provides its configuration facts."""


def _read() -> Dict[str, Any]:
    raw = os.environ.get(_ENV, "")
    if not raw:
        raise HostInfoUnavailable("the PiG host did not provide its configuration facts (PIG_EXT_HOST_INFO)")
    try:
        info = json.loads(raw)
    except ValueError as error:
        raise HostInfoUnavailable("PIG_EXT_HOST_INFO is not valid JSON: %s" % error) from error
    if not isinstance(info, dict):
        raise HostInfoUnavailable("PIG_EXT_HOST_INFO is not a JSON object")
    return info


def get_agent_dir() -> str:
    """Pi's ``getAgentDir()``: the agent configuration directory the host runs with (for PiG its own tree, not Pi's). Raises
    :class:`HostInfoUnavailable` when the process was not started by a host that provides it, or the host could not resolve the directory."""
    agent_dir = _read().get("agentDir", "")
    if not agent_dir:
        raise HostInfoUnavailable("the PiG host could not resolve its agent directory")
    return str(agent_dir)


def get_config_dir_name() -> str:
    """Pi's ``CONFIG_DIR_NAME``: the per-project configuration directory name the host uses (".pig"; ".pi" when it shares Pi's tree)."""
    return str(_read().get("configDirName", ""))


def get_version() -> str:
    """Pi's ``VERSION``: the Pi version the host implements, which extensions compare against to gate features."""
    return str(_read().get("version", ""))
