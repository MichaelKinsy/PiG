import pytest

import pig_sdk


@pytest.mark.parametrize("schema", [None, [], "object", 1, False])
def test_tool_schema_validation_precedes_registration(schema):
    ext = pig_sdk.Extension("schema")
    with pytest.raises(ValueError) as failure:
        ext.tool("noop", "No-op", schema, lambda ctx, args: {"content": "bad"})
    assert str(failure.value) == 'Tool "noop" registered by extension "schema" must define an object parameter schema.'
    assert ext._tools == []
    assert ext._tool_handlers == {}
    assert ext._tool_prepare_handlers == {}
    ext.tool("noop", "No-op", {}, lambda ctx, args: {"content": "ok"})
    assert [tool["parameters"] for tool in ext._tools] == [{}]
    assert list(ext._tool_handlers) == ["noop"]


def test_tool_sampling_false_presence():
    # Pi ToolDefinition.constrainedSampling is false | config | undefined: false stays present, omission stays absent.
    ext = pig_sdk.Extension("sampling")
    ext.tool("unset", "Unset", {"type": "object"}, lambda ctx, params: "ok")
    ext.tool("disabled", "Disabled", {"type": "object"}, lambda ctx, params: "ok", constrained_sampling=False)
    assert "constrained_sampling" not in ext._tools[0]
    assert ext._tools[1]["constrained_sampling"] is False
