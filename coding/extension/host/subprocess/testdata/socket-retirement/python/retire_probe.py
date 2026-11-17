# The Python fixture of the socket retirement rows (socket_retirement_runtime_test.go). The test names the gate directory in PIG_RETIREMENT_GATES.
import os
import time

import pig_sdk


def _touch(name):
    with open(os.path.join(os.environ["PIG_RETIREMENT_GATES"], name), "w"):
        pass


def _exists(name):
    return os.path.exists(os.path.join(os.environ["PIG_RETIREMENT_GATES"], name))


def _hold(ctx, _args):
    ctx.reload()
    _touch("reloaded")
    while not _exists("release-command"):
        time.sleep(0.005)


def new_extension() -> pig_sdk.Extension:
    # The factory holds while the test keeps the factory gate, before the SDK connects to the host.
    if _exists("factory-hold"):
        _touch("factory-holding")
        while _exists("factory-hold"):
            time.sleep(0.005)
    ext = pig_sdk.Extension("retire-probe")
    ext.command("retire-hold", "reload, then hold the command", _hold)
    ext.command("retire-ping", "answer from this generation", lambda _ctx, _args: _touch("pinged"))
    return ext


if __name__ == "__main__":
    new_extension().run()
