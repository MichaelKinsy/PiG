use pig_sdk::{Extension,ModelEventStream};
use serde_json::json;
use std::sync::Arc;

pub fn new_extension()->Extension{
 let mut extension=Extension::new("provider-producer");
 extension.register_provider_stream("extension-provider",json!({"api":"issue-8964-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,request,options|{
  if options["apiKey"]!="extension-key"{return Err("wrong key".into())}
  if request["messages"].as_array().and_then(|messages|messages.last()).and_then(|message|message.get("content")).and_then(|content|content.as_str().or_else(||content.get(0).and_then(|block|block.get("text")).and_then(|text|text.as_str())))==Some("cancel"){ctx.exec("provider-started", &[])?;while !ctx.is_cancelled(){std::thread::sleep(std::time::Duration::from_millis(1));}return Err("provider stream aborted".into());}
  let message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":"custom provider response"}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  for event in [json!({"type":"start","partial":message}),json!({"type":"text_start","contentIndex":0,"partial":message}),json!({"type":"text_delta","contentIndex":0,"delta":"custom provider response","partial":message}),json!({"type":"text_end","contentIndex":0,"content":"custom provider response","partial":message}),json!({"type":"done","reason":"stop","message":message})]{stream.push(event)}
  Ok(stream)
 });extension
}
