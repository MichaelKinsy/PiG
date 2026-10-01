import pytest

import pig_sdk

# .upstream/v0.99.2/packages/coding-agent/src/core/extensions/loader.ts:302-311 (#10054): registerCommand rejects a
# command without a non-empty string name or without a handler function before it registers, so the extension fails to
# load instead of crashing pi when `/` lists its commands.


def noop(ctx, args):
    return None


@pytest.mark.parametrize("name", ["", None, 1])
def test_command_name_validation_precedes_registration(name):
    ext = pig_sdk.Extension("commands")
    with pytest.raises(ValueError) as failure:
        ext.command(name, "d", noop)
    assert str(failure.value) == 'Command registered by extension "commands" must have a non-empty string name. Use pi.registerCommand("name", { description, handler }).'
    assert ext._commands == []
    assert ext._command_handlers == {}
    assert ext._command_completions == {}
    ext.command("noop", "d", noop)
    assert [command["name"] for command in ext._commands] == ["noop"]


@pytest.mark.parametrize("handler", [None, "handler", 1])
def test_command_handler_validation_precedes_registration(handler):
    ext = pig_sdk.Extension("commands")
    with pytest.raises(ValueError) as failure:
        ext.command("noop", "d", handler, get_argument_completions=lambda prefix: None)
    assert str(failure.value) == 'Command "/noop" registered by extension "commands" must define handler().'
    assert ext._commands == []
    assert ext._command_handlers == {}
    assert ext._command_completions == {}
