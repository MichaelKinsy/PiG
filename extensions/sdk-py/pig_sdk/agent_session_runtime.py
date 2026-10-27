"""Pi's ``createAgentSessionRuntime`` and ``AgentSessionRuntime`` (core/agent-session-runtime.ts).

The runtime owns the current ``AgentSession`` plus its cwd-bound services. The factory that builds them stays in this process and is called on every session replacement, as Pi's is. The host answers the calls the runtime makes on the session it replaces: the session_before_switch, session_before_fork and session_shutdown events through the session's extensions, ``refresh_context`` and the session's log. The replacement methods tear the current runtime down first, then create and apply the next one; a failing factory's error is raised and the caller handles it.

Async contract (Pi's promises, kept as blocking calls): every replacement method returns once the new session is applied and rebound; a failure raises. Pi's ``withSession`` (it takes the ``ReplacedSessionContext`` of ``AgentSession.createReplacedSessionContext``, a C2Sessions gap) and ``projectTrustContextFactory`` (a record of the host's UI functions) are not served: a value for either raises before anything happens.
"""
from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from typing import Any, Callable

from .agent_session_services import AgentSessionServices
from .child_session import AgentSession, SessionStartEvent
from .session_manager import SessionManager

_WITH_SESSION = (
    "the with_session option is not supported yet: it takes the ReplacedSessionContext of AgentSession.createReplacedSessionContext, which is a C2Sessions gap in test/parity/sdk-exports.toml; "
    "use the setup option, or read AgentSessionRuntime.session once the call returned"
)
_TRUST_FACTORY = "the project_trust_context_factory option is not supported: it builds a record of the host's UI functions, which cannot cross the process boundary"


class SessionImportFileNotFoundError(Exception):
    """Pi's ``SessionImportFileNotFoundError``: ``import_from_jsonl`` named a file that does not exist."""

    def __init__(self, file_path: str) -> None:
        super().__init__(f"File not found: {file_path}")
        self.file_path = file_path


class MissingSessionCwdError(Exception):
    """Pi's ``MissingSessionCwdError``: the working directory a session file stored does not exist."""

    def __init__(self, session_file: str, session_cwd: str, fallback_cwd: str) -> None:
        file = f"\nSession file: {session_file}" if session_file else ""
        super().__init__(f"Stored session working directory does not exist: {session_cwd}{file}\nCurrent working directory: {fallback_cwd}")
        self.session_file, self.session_cwd, self.fallback_cwd = session_file, session_cwd, fallback_cwd


@dataclass(kw_only=True)
class CreateAgentSessionRuntimeResult:
    """Pi's CreateAgentSessionRuntimeResult: the session the factory created, its cwd-bound services and the diagnostics collected while setting up."""

    session: AgentSession
    services: AgentSessionServices
    diagnostics: list[dict[str, str]] = field(default_factory=list)
    #: ``None`` is Pi's undefined.
    model_fallback_message: str | None = None


@dataclass(kw_only=True)
class CreateAgentSessionRuntimeFactoryOptions:
    """The argument of Pi's CreateAgentSessionRuntimeFactory: the target of the session to build. Pi's ``projectTrustContext`` is not passed."""

    cwd: str
    agent_dir: str
    session_manager: SessionManager
    session_start_event: SessionStartEvent | None = None


@dataclass(kw_only=True)
class CreateAgentSessionRuntimeOptions:
    """The second argument of Pi's createAgentSessionRuntime."""

    cwd: str
    agent_dir: str
    session_manager: SessionManager
    session_start_event: SessionStartEvent | None = None


def _session_file(manager: SessionManager) -> str | None:
    return manager.get_session_file()


def _assert_session_cwd_exists(manager: SessionManager, fallback_cwd: str) -> None:
    """Pi's assertSessionCwdExists: a persisted session whose stored working directory is gone raises a MissingSessionCwdError."""
    session_file = _session_file(manager)
    if not session_file:
        return
    session_cwd = manager.get_cwd()
    if session_cwd and not os.path.exists(session_cwd):
        raise MissingSessionCwdError(session_file, session_cwd, fallback_cwd)


def _user_message_text(content: Any) -> str:
    """extractUserMessageText: a string, or the text parts of a content array joined."""
    if isinstance(content, str):
        return content
    return "".join(part["text"] for part in content or [] if isinstance(part, dict) and part.get("type") == "text" and isinstance(part.get("text"), str))


def _resolve_path(path: str) -> str:
    return os.path.abspath(os.path.expanduser(path.strip()))


class AgentSessionRuntime:
    def __init__(self, context: Any, create_runtime: Callable[[CreateAgentSessionRuntimeFactoryOptions], CreateAgentSessionRuntimeResult]) -> None:
        self._context = context
        self._create_runtime = create_runtime
        self._rebind_session: Callable[[AgentSession], None] | None = None
        self._before_session_invalidate: Callable[[], None] | None = None
        self._session: AgentSession
        self._services: AgentSessionServices
        self._diagnostics: list[dict[str, str]] = []
        self._model_fallback_message: str | None = None

    @property
    def services(self) -> AgentSessionServices:
        """``runtime.services``: the current cwd-bound services."""
        return self._services

    @property
    def session(self) -> AgentSession:
        """``runtime.session``: the current session."""
        return self._session

    @property
    def cwd(self) -> str:
        """``runtime.cwd``: the working directory of the current services."""
        return self._services.cwd

    @property
    def diagnostics(self) -> list[dict[str, str]]:
        """``runtime.diagnostics``."""
        return self._diagnostics

    @property
    def model_fallback_message(self) -> str | None:
        """``runtime.modelFallbackMessage``."""
        return self._model_fallback_message

    def set_rebind_session(self, rebind_session: Callable[[AgentSession], None] | None = None) -> None:
        """``setRebindSession(rebindSession?)``: the function runs with the new session after every replacement. ``None`` removes it."""
        self._rebind_session = rebind_session

    def set_before_session_invalidate(self, before_session_invalidate: Callable[[], None] | None = None) -> None:
        """``setBeforeSessionInvalidate(callback?)``: the function runs after the current session's session_shutdown handlers finished and before the session is disposed. ``None`` removes it."""
        self._before_session_invalidate = before_session_invalidate

    def _apply(self, result: CreateAgentSessionRuntimeResult) -> None:
        self._session, self._services = result.session, result.services
        self._diagnostics, self._model_fallback_message = result.diagnostics, result.model_fallback_message

    def _emit_before(self, event: dict[str, Any]) -> bool:
        result, emitted = self._session._emit_extension_event(event)
        return bool(emitted and isinstance(result, dict) and result.get("cancel") is True)

    def _emit_before_switch(self, reason: str, target_session_file: str | None = None) -> bool:
        event: dict[str, Any] = {"type": "session_before_switch", "reason": reason}
        if target_session_file:
            event["targetSessionFile"] = target_session_file
        return self._emit_before(event)

    def _teardown_current(self, reason: str, target_session_file: str | None = None) -> None:
        # Settle any active response first so the aborted turn (including tool results) is persisted to the outgoing session before it is replaced.
        self._session.abort()
        event: dict[str, Any] = {"type": "session_shutdown", "reason": reason}
        if target_session_file:
            event["targetSessionFile"] = target_session_file
        self._session._emit_extension_event(event)
        if self._before_session_invalidate is not None:
            self._before_session_invalidate()
        self._session.dispose()

    def _finish_session_replacement(self) -> None:
        if self._rebind_session is not None:
            self._rebind_session(self._session)

    def _replace(self, cwd: str, manager: SessionManager, reason: str, previous_session_file: str | None) -> None:
        self._apply(
            self._create_runtime(
                CreateAgentSessionRuntimeFactoryOptions(
                    cwd=cwd, agent_dir=self._services.agent_dir, session_manager=manager, session_start_event=SessionStartEvent(reason=reason, previous_session_file=previous_session_file)
                )
            )
        )

    def switch_session(self, session_path: str, options: dict[str, Any] | None = None) -> dict[str, bool]:
        """``switchSession(sessionPath, options?)``: ``options["cwdOverride"]`` is the working directory to use when the stored one is gone."""
        options = options or {}
        if options.get("withSession") is not None:
            raise RuntimeError(_WITH_SESSION)
        if options.get("projectTrustContextFactory") is not None:
            raise RuntimeError(_TRUST_FACTORY)
        if self._emit_before_switch("resume", session_path):
            return {"cancelled": True}
        previous = _session_file(self._session.session_manager())
        manager = SessionManager.open(self._context, session_path, None, options.get("cwdOverride"))
        _assert_session_cwd_exists(manager, self.cwd)
        self._teardown_current("resume", _session_file(manager))
        self._replace(manager.get_cwd(), manager, "resume", previous)
        self._finish_session_replacement()
        return {"cancelled": False}

    def new_session(self, options: dict[str, Any] | None = None) -> dict[str, bool]:
        """``newSession(options?)``: ``options["parentSession"]`` is the parent session file and ``options["setup"]`` a function that runs with the new session's log before the session reads it (the session's context is refreshed after it)."""
        options = options or {}
        if options.get("withSession") is not None:
            raise RuntimeError(_WITH_SESSION)
        if self._emit_before_switch("new"):
            return {"cancelled": True}
        current = self._session.session_manager()
        previous = _session_file(current)
        session_dir = current.get_session_dir()
        manager = SessionManager.create(self._context, self.cwd, session_dir) if current.is_persisted() else SessionManager.in_memory(self._context, self.cwd)
        if options.get("parentSession"):
            manager.new_session({"parentSession": options["parentSession"]})
        self._teardown_current("new", _session_file(manager))
        self._replace(self.cwd, manager, "new", previous)
        if options.get("setup") is not None:
            options["setup"](self._session.session_manager())
            self._session.refresh_context()
        self._finish_session_replacement()
        return {"cancelled": False}

    def fork(self, entry_id: str, options: dict[str, Any] | None = None) -> dict[str, Any]:
        """``fork(entryId, options?)``: ``options["position"]`` is ``"before"`` (the default) or ``"at"``. The result is ``{"cancelled": bool}`` and, when the fork was before a user message, ``"selectedText"``."""
        options = options or {}
        if options.get("withSession") is not None:
            raise RuntimeError(_WITH_SESSION)
        position = options.get("position") or "before"
        if self._emit_before({"type": "session_before_fork", "entryId": entry_id, "position": position}):
            return {"cancelled": True}
        current = self._session.session_manager()
        entry = current.get_entry(entry_id)
        if entry is None:
            raise RuntimeError("Invalid entry ID for forking")
        selected_text: str | None = None
        if position == "at":
            target_leaf_id = entry["id"]
        else:
            message = entry.get("message") or {}
            if entry.get("type") != "message" or message.get("role") != "user":
                raise RuntimeError("Invalid entry ID for forking")
            target_leaf_id = entry.get("parentId")
            selected_text = _user_message_text(message.get("content"))
        previous = _session_file(current)

        def finish(manager: SessionManager, cwd: str) -> dict[str, Any]:
            self._replace(cwd, manager, "fork", previous)
            self._finish_session_replacement()
            return {"cancelled": False, **({"selectedText": selected_text} if selected_text is not None else {})}

        if current.is_persisted():
            if not previous:
                raise RuntimeError("Persisted session is missing a session file")
            session_dir = current.get_session_dir()
            if not target_leaf_id:
                manager = SessionManager.create(self._context, self.cwd, session_dir)
                manager.new_session({"parentSession": previous})
                self._teardown_current("fork", _session_file(manager))
                return finish(manager, self.cwd)
            if not os.path.exists(previous):
                raise RuntimeError("This session has not been saved yet. Send a message before cloning or forking it.")
            manager = SessionManager.open(self._context, previous, session_dir)
            if not manager.create_branched_session(target_leaf_id):
                raise RuntimeError("Failed to create forked session")
            self._teardown_current("fork", _session_file(manager))
            return finish(manager, manager.get_cwd())
        self._teardown_current("fork", _session_file(current))
        if not target_leaf_id:
            current.new_session({"parentSession": previous} if previous else None)
        else:
            current.create_branched_session(target_leaf_id)
        return finish(current, self.cwd)

    def import_from_jsonl(self, input_path: str, cwd_override: str | None = None) -> dict[str, bool]:
        """``importFromJsonl(inputPath, cwdOverride?)``: it copies the session file into the current session directory (unless it is already there) and switches to it. The file's absence raises a ``SessionImportFileNotFoundError``."""
        resolved = _resolve_path(input_path)
        if not os.path.exists(resolved):
            raise SessionImportFileNotFoundError(resolved)
        session_dir = self._session.session_manager().get_session_dir()
        if not os.path.exists(session_dir):
            os.makedirs(session_dir, exist_ok=True)
        destination = os.path.join(session_dir, os.path.basename(resolved))
        already_stored = os.path.abspath(destination) == resolved
        if not already_stored:
            name, ext = os.path.splitext(os.path.basename(destination))
            suffix = 1
            while os.path.exists(destination):
                destination = os.path.join(session_dir, f"{name}-{suffix}{ext}")
                suffix += 1
        if self._emit_before_switch("resume", destination):
            return {"cancelled": True}
        previous = _session_file(self._session.session_manager())
        if not already_stored:
            with open(resolved, "rb") as source, open(destination, "xb") as target:
                shutil.copyfileobj(source, target)
        manager = SessionManager.open(self._context, destination, session_dir, cwd_override)
        _assert_session_cwd_exists(manager, self.cwd)
        self._teardown_current("resume", _session_file(manager))
        self._replace(manager.get_cwd(), manager, "resume", previous)
        self._finish_session_replacement()
        return {"cancelled": False}

    def dispose(self) -> None:
        """``dispose()``: it emits session_shutdown with the reason ``"quit"`` to the current session's extensions, runs the before-invalidate callback and disposes the session."""
        self._session._emit_extension_event({"type": "session_shutdown", "reason": "quit"})
        if self._before_session_invalidate is not None:
            self._before_session_invalidate()
        self._session.dispose()


def create_agent_session_runtime(
    context: Any,
    create_runtime: Callable[[CreateAgentSessionRuntimeFactoryOptions], CreateAgentSessionRuntimeResult],
    options: CreateAgentSessionRuntimeOptions,
) -> AgentSessionRuntime:
    """``createAgentSessionRuntime(createRuntime, options)``: it builds the initial runtime from the factory and keeps the factory for the replacements."""
    _assert_session_cwd_exists(options.session_manager, options.cwd)
    runtime = AgentSessionRuntime(context, create_runtime)
    runtime._apply(
        create_runtime(
            CreateAgentSessionRuntimeFactoryOptions(cwd=options.cwd, agent_dir=options.agent_dir, session_manager=options.session_manager, session_start_event=options.session_start_event)
        )
    )
    return runtime
