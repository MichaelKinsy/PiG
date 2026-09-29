use pig_sdk::{
    ConstrainedSampling, Extension, ToolConstrainedSampling, ToolDefinition, ToolResult,
};
use serde_json::json;

// Pi ToolDefinition.constrainedSampling is false | ConstrainedSamplingConfig | undefined, not bool | config.
#[test]
fn constrained_sampling_accepts_only_false_or_config() {
    let disabled: ToolConstrainedSampling = serde_json::from_value(json!(false)).unwrap();
    assert_eq!(serde_json::to_value(disabled).unwrap(), json!(false));
    assert!(serde_json::from_value::<ToolConstrainedSampling>(json!(true)).is_err());
    for value in [
        json!({"type":"json_schema","strict":"prefer"}),
        json!({"type":"grammar","variants":{"openai_lark":"start: NUMBER"}}),
    ] {
        let decoded: ToolConstrainedSampling = serde_json::from_value(value.clone()).unwrap();
        assert_eq!(serde_json::to_value(decoded).unwrap(), value);
    }
}

#[test]
fn tool_definitions_can_explicitly_disable_sampling() {
    let mut ext = Extension::new("sampling");
    let mut tool = ToolDefinition::new(
        "disabled",
        "Disabled",
        "Disable sampling",
        json!({"type":"object"}),
        |_, _| ToolResult::text("ok"),
    );
    tool.constrained_sampling = Some(ToolConstrainedSampling::Disabled);
    ext.register_tool(tool);
    ext.tool_with_constrained_sampling(
        "disabled_helper",
        "Disable sampling",
        json!({"type":"object"}),
        ToolConstrainedSampling::Disabled,
        |_, _| ToolResult::text("ok"),
    );
    ext.tool_with_constrained_sampling(
        "configured_helper",
        "Configure sampling",
        json!({"type":"object"}),
        ConstrainedSampling {
            kind: "json_schema".into(),
            strict: Some("prefer".into()),
            variants: None,
        },
        |_, _| ToolResult::text("ok"),
    );
}
