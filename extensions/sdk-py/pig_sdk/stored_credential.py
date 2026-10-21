"""Pi's readStoredCredential (core/auth-storage.ts, exported from index.ts)."""
import json
import os
from typing import Any, Dict, Optional

from .host_info import get_agent_dir


def _normalize_path(path: str) -> str:
    """The part of Pi's normalizePath (utils/paths.ts) that read_stored_credential's path can use: a leading ~ is the home directory and a file:// URL is its path."""
    if path == "~" or path.startswith("~/"):
        return os.path.join(os.path.expanduser("~"), path[2:])
    if path.startswith("file://"):
        return path[len("file://"):]
    return path


def read_stored_credential(provider_id: str, auth_path: Optional[str] = None) -> Optional[Dict[str, Any]]:
    """Pi's readStoredCredential: a one-off synchronous read of the credential stored for ``provider_id`` in an auth.json file, without a store and without resolving
    configured key values. ``auth_path`` defaults to auth.json in the agent directory (:func:`get_agent_dir`); a leading ~ in it is the home directory and a file:// URL
    is its path. A file that cannot be read or is not a JSON object, and a provider it has no credential for, give None. The credential keeps the key order of the file.
    Raises :class:`HostInfoUnavailable` when there is no ``auth_path`` and the host gave no agent directory, which Pi's default argument throws."""
    path = auth_path if auth_path is not None else os.path.join(get_agent_dir(), "auth.json")
    try:
        with open(_normalize_path(path), encoding="utf-8") as stream:
            text = stream.read()
    except (OSError, ValueError):
        return None
    if text.startswith("\ufeff"):
        text = text[1:]
    try:
        data = json.loads(text)
    except ValueError:
        return None
    if not isinstance(data, dict):
        return None
    return data.get(provider_id)
