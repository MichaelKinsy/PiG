"""Pi's ``getAgentDir()``, ``CONFIG_DIR_NAME``, ``VERSION`` and ``getPackageDir()`` (config.ts) and the default ``getShellConfig()`` (utils/shell.ts) as the PiG host resolves them.

They are callable while an extension's factory runs, before the host's ready message, so the host starts every extension process with them in
``PIG_EXT_HOST_INFO``.
"""
import json
import os
from dataclasses import dataclass
from typing import Any, Dict, Optional, Tuple

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


def get_package_dir() -> str:
    """Pi's ``getPackageDir()``: the install directory the host runs from. For PiG it is PI_PACKAGE_DIR when that is set, else the directory of the host executable, which is
    Pi's own answer for a compiled binary. Raises :class:`HostInfoUnavailable` when the process was not started by a host that provides it, or the host could not resolve it."""
    package_dir = _read().get("packageDir", "")
    if not package_dir:
        raise HostInfoUnavailable("the PiG host could not resolve its package directory")
    return str(package_dir)


@dataclass(frozen=True)
class ShellConfig:
    """Pi's ShellConfig (utils/shell.ts): the shell binary a command runs in and its leading arguments. The command is appended as the last argument unless
    ``command_transport`` is "stdin", in which case it is written to the shell's standard input (legacy WSL bash.exe)."""

    shell: str
    args: Tuple[str, ...]
    command_transport: Optional[str] = None


_LEGACY_WSL_BASH = (":\\windows\\system32\\bash.exe", ":\\windows\\sysnative\\bash.exe")


def _is_legacy_wsl_bash_path(shell: str) -> bool:
    """isLegacyWslBashPath: /^[a-z]:\\windows\\(?:system32|sysnative)\\bash\\.exe$/ on the lowercased path with / as \\. Only an ASCII letter, or the Kelvin sign that
    lowercases to k, can be the drive."""
    if not shell or not (shell[0].isascii() and shell[0].isalpha() or shell[0] == "\u212a"):
        return False
    rest = shell[1:].replace("/", "\\")
    folded = "".join(chr(ord(c) + 32) if "A" <= c <= "Z" else c for c in rest)
    return folded in _LEGACY_WSL_BASH


def get_shell_config(custom_shell_path: Optional[str] = None) -> ShellConfig:
    """Pi's ``getShellConfig(customShellPath)``: the shell the bash tool runs commands in. A non-empty ``custom_shell_path`` names the shell and must exist (``OSError``
    "Custom shell path not found" otherwise). With none, it is the host's own resolution: /bin/bash, then bash on PATH, then sh on Unix; Git Bash, then bash.exe on PATH on
    Windows, which raises :class:`OSError` when none is found. Raises :class:`HostInfoUnavailable` when the host provides no default."""
    if custom_shell_path:
        if not os.path.exists(custom_shell_path):
            raise OSError("Custom shell path not found: " + custom_shell_path)
        if _is_legacy_wsl_bash_path(custom_shell_path):
            return ShellConfig(custom_shell_path, ("-s",), "stdin")
        return ShellConfig(custom_shell_path, ("-c",))
    shell = _read().get("shell")
    if not isinstance(shell, dict):
        raise HostInfoUnavailable("the PiG host did not provide a default shell")
    if shell.get("error"):
        raise OSError(str(shell["error"]))
    return ShellConfig(str(shell.get("shell", "")), tuple(shell.get("args") or ()), shell.get("commandTransport") or None)
