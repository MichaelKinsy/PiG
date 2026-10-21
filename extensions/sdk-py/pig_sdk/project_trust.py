"""Pi's ``ProjectTrustStore`` class and ``hasTrustRequiringProjectResources`` (core/trust-manager.ts). The host owns the store, so a decision saved here is the one the project trust gate reads. A decision is Pi's ProjectTrustDecision: ``True``, ``False`` or ``None`` (undecided)."""
from __future__ import annotations

from typing import Any

from ._graph import _GraphObject


class ProjectTrustStore(_GraphObject):
    def __init__(self, context: Any, agent_dir: str) -> None:
        """``new ProjectTrustStore(agentDir)``: the decisions saved in ``<agent_dir>/trust.json``."""
        super().__init__(context, "ProjectTrustStore", "new", agent_dir)

    def get(self, cwd: str) -> bool | None:
        """The nearest saved decision for ``cwd`` or one of its parents, ``None`` when there is none."""
        return self._invoke("get", cwd)

    def get_entry(self, cwd: str) -> dict[str, Any] | None:
        """The nearest saved decision with the directory that holds it (``{"path", "decision"}``), ``None`` when there is none."""
        return self._invoke("getEntry", cwd)

    def set(self, cwd: str, decision: bool | None) -> None:
        """Save the decision for ``cwd``; ``None`` removes it."""
        self._invoke("set", cwd, decision)

    def set_many(self, decisions: list[dict[str, Any]]) -> None:
        """Apply several ``{"path", "decision"}`` updates in one locked update of the store."""
        self._invoke("setMany", decisions)


def has_trust_requiring_project_resources(context: Any, cwd: str) -> bool:
    """Pi's ``hasTrustRequiringProjectResources(cwd)``: whether ``cwd`` holds project-local resources that project trust gates (entries of its configuration directory, or ``.agents/skills`` in ``cwd`` or an ancestor other than the user's)."""
    return _GraphObject._static(context, "ProjectTrustStore", "hasTrustRequiringProjectResources", cwd)
