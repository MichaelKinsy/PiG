use crate::context::ModelInfo;
use serde_json::json;

fn summary(model: &ModelInfo) -> String {
    format!(
        "{}/{}/{}/{}/{}/{}/{}/{}/{}/{}",
        model.id, model.name, model.provider, model.context_window, model.max_output_tokens, model.reasoning, model.input_cost_per_1m, model.output_cost_per_1m, model.cache_read_cost_per_1m, model.cache_write_cost_per_1m
    )
}

fn decode(value: serde_json::Value) -> Option<String> {
    ModelInfo::from_wire(&value).unwrap().map(|model| summary(&model))
}

// The host's reply is Pi's Model: maxTokens and a cost object (packages/ai/src/types.ts).
#[test]
fn a_pi_model_reports_its_output_limit_and_prices() {
    let model = json!({"provider":"anthropic","id":"m1","name":"M One","reasoning":true,"contextWindow":200000,"maxTokens":64000,"cost":{"input":3,"output":15,"cacheRead":0.3,"cacheWrite":3.75}});
    assert_eq!(decode(model).unwrap(), "m1/M One/anthropic/200000/64000/true/3/15/0.3/3.75");
}

#[test]
fn the_flat_keys_win_over_pi_keys_only_when_non_zero_and_every_variant_reads_as_in_go() {
    assert_eq!(decode(json!({"id":"a","maxOutputTokens":10,"maxTokens":20,"inputCostPer1M":1,"cost":{"input":9,"output":8}})).unwrap(), "a///0/10/false/1/8/0/0");
    assert_eq!(decode(json!({"id":"a","maxOutputTokens":0,"maxTokens":20})).unwrap(), "a///0/20/false/0/0/0/0");
    assert_eq!(decode(json!({"modelId":"b","displayName":"B","provider":{"id":"p"}})).unwrap(), "b/B/p/0/0/false/0/0/0/0");
    assert_eq!(decode(json!({"id":"c","name":"C","displayName":"ignored","modelId":"ignored","provider":null})).unwrap(), "c/C//0/0/false/0/0/0/0");
    assert_eq!(decode(json!({"id":"d","contextWindow":1000000,"maxTokens":2048})).unwrap(), "d///1000000/2048/false/0/0/0/0");
    assert_eq!(decode(json!({"id":"f","provider":7})).unwrap(), "f///0/0/false/0/0/0/0");
}

#[test]
fn a_reply_that_names_no_model_is_none() {
    assert!(decode(json!({})).is_none());
    assert!(decode(json!({"id":"","modelId":"","name":"x"})).is_none());
    assert!(decode(json!(null)).is_none());
}

// A field of the wrong type is an error, as Go's decode reports it, not a zero.
#[test]
fn a_field_of_the_wrong_type_is_an_error() {
    for bad in [
        json!({"id":"e","cost":{"input":"cheap"}}),
        json!({"id":"e","cost":"free"}),
        json!({"id":"e","contextWindow":"big"}),
        json!({"id":"e","contextWindow":1.5}),
        json!({"id":"e","maxTokens":"many"}),
        json!({"id":"e","maxOutputTokens":5,"maxTokens":"many"}),
        json!({"id":"e","reasoning":"yes"}),
        json!({"id":5}),
        json!({"id":"e","name":["x"]}),
        json!({"id":"e","inputCostPer1M":"x"}),
        json!({"id":"e","name":"N","displayName":5}),
        json!({"id":"e","inputLimits":5}),
        json!({"id":"e","contextWindow":1.0}),
        json!({"id":"e","contextWindow":1e3}),
        json!("x"),
        json!(5),
        json!([]),
    ] {
        assert!(ModelInfo::from_wire(&bad).is_err(), "{bad}");
    }
}

#[test]
fn input_limits_are_carried_through() {
    let model = ModelInfo::from_wire(&json!({"id":"g","inputLimits":{"maxRequestBytes":1}})).unwrap().unwrap();
    assert_eq!(model.input_limits, Some(json!({"maxRequestBytes":1})));
    assert!(ModelInfo::from_wire(&json!({"id":"g","inputLimits":null})).unwrap().unwrap().input_limits.is_none());
}

// ModelInfo's integer fields are i32, where Go's are int: a count past i32 is an error rather than a wrapped number.
#[test]
fn a_count_past_i32_is_an_error_not_a_wrapped_number() {
    assert!(ModelInfo::from_wire(&json!({"id":"e","maxTokens":4294967296u64})).is_err());
    assert!(ModelInfo::from_wire(&json!({"id":"e","contextWindow":2147483648u64})).is_err());
    assert_eq!(ModelInfo::from_wire(&json!({"id":"e","contextWindow":2147483647})).unwrap().unwrap().context_window, i32::MAX);
}
