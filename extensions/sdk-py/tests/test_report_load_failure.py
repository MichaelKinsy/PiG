"""``report_load_failure``: the packed runner's report that a member's factory failed ends the host's wait for it, also when the factory called a host helper
first (which dials the host and keeps the connection for ``run``)."""
import os
import socket
import tempfile

import pytest

from pig_sdk import host_helpers


def _listener():
    directory = tempfile.mkdtemp(prefix="rlf")
    path = os.path.join(directory, "s.sock")
    server = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    server.bind(path)
    server.listen(4)
    server.settimeout(5)
    return directory, path, server


@pytest.mark.skipif(not hasattr(socket, "AF_UNIX"), reason="needs AF_UNIX sockets")
def test_report_load_failure_closes_the_connection_a_helper_dialled():
    directory, path, server = _listener()
    try:
        helper = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        helper.connect(path)
        host_helpers._State.dialled[path] = helper
        accepted, _ = server.accept()
        accepted.settimeout(5)
        host_helpers.report_load_failure(path)
        assert accepted.recv(1) == b""  # the host sees the helper's connection end
        assert path not in host_helpers._State.dialled
        server.settimeout(0.2)
        with pytest.raises(socket.timeout):
            server.accept()  # and no second connection was made
    finally:
        server.close()
        os.remove(path)
        os.rmdir(directory)


@pytest.mark.skipif(not hasattr(socket, "AF_UNIX"), reason="needs AF_UNIX sockets")
def test_report_load_failure_connects_and_closes_when_no_helper_dialled():
    directory, path, server = _listener()
    try:
        host_helpers.report_load_failure(path)
        accepted, _ = server.accept()
        accepted.settimeout(5)
        assert accepted.recv(1) == b""
    finally:
        server.close()
        os.remove(path)
        os.rmdir(directory)
