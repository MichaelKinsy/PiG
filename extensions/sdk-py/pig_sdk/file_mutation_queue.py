"""Pi's ``withFileMutationQueue`` (core/tools/file-mutation-queue.ts) for an extension that runs in a process of its own.

Pi has one queue in one process, shared by the built-in edit and write tools and every extension. Here the queue lives in the host: the SDK asks it for the
file's turn (``fileMutation.enter``, answered once the file's earlier mutations completed) and gives the turn back (``fileMutation.exit``). The SDK keeps no
queue and no path-key code of its own.
"""
import os
from typing import Callable, TypeVar

from . import host_helpers

T = TypeVar("T")

_CALL_FILE_MUTATION_ENTER = "fileMutation.enter"
_CALL_FILE_MUTATION_EXIT = "fileMutation.exit"


def with_file_mutation_queue(file_path: str, fn: Callable[[], T]) -> T:
    """Run ``fn`` once every mutation registered earlier for the same file has finished, then let the next one run, and return its result.

    Mutations of different files run in parallel. Edit and write extension tools wrap their read-modify-write in it so parallel tool calls on one
    file cannot interleave. The file is keyed by its real path (a symlink and its target share a queue; a path that does not exist yet is keyed by
    its absolute path) and callers run in the order they called. A path that cannot be resolved for a reason other than a missing file or directory
    raises the host's ``HostCallError`` without running ``fn``.

    The queue is the host's: it serializes this extension's mutations with the host's built-in edit and write tools and with every other extension
    process, as Pi's one queue does. The turn is held by the extension's process: a packed cell's members share it, so one member stopping while another's
    ``fn`` runs does not release the file, and the host releases it when the process's last connection closes. Like the other host helpers it may be called before ``run``; with no connection to the host (``PIG_EXT_SOCKET`` unset) it raises
    ``RuntimeError`` without running ``fn``.
    """
    # Pi resolves the path against the process's working directory; the host resolves nothing relative, so the SDK sends it absolute.
    entered = host_helpers._helper_call(_CALL_FILE_MUTATION_ENTER, {"path": os.path.abspath(file_path)})
    ticket = entered.get("ticket")
    if not ticket:
        raise RuntimeError("fileMutation.enter returned no ticket")
    try:
        return fn()
    finally:
        try:
            host_helpers._helper_call(_CALL_FILE_MUTATION_EXIT, {"ticket": ticket})
        except (RuntimeError, OSError, EOFError):
            # The turn belongs to the process, not to the connection the exit rode: when that connection is stopping (its extension was replaced or stopped while
            # this task ran), another running extension's connection gives the turn back.
            try:
                host_helpers._call_each_running(_CALL_FILE_MUTATION_EXIT, {"ticket": ticket})
            except (RuntimeError, OSError, EOFError):
                # With no connection of the process left the host has released the turn already (it does when the last connection closes).
                pass
