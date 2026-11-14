use pig_sdk::{Extension, ModelEventStream};
use serde_json::json;
use std::sync::Arc;

pub fn new_extension() -> Extension {
    let mut extension = Extension::new("provider-producer");
    // A provider whose model names an api that no extension registers and that has no streamSimple.
    extension.register_provider("unknown-api-provider",json!({"api":"unknown-api-no-extension-registered","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}));
    extension.register_provider_stream("extension-provider",json!({"api":"issue-8964-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,request,options|{
  if options["apiKey"]!="extension-key"{return Err("wrong key".into())}
  if request["messages"].as_array().and_then(|messages|messages.last()).and_then(|message|message.get("content")).and_then(|content|content.as_str().or_else(||content.get(0).and_then(|block|block.get("text")).and_then(|text|text.as_str())))==Some("cancel"){ctx.exec("provider-started", &[])?;while !ctx.is_cancelled(){std::thread::sleep(std::time::Duration::from_millis(1));}return Err("provider stream aborted".into());}
  let message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":"custom provider response"}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  for event in [json!({"type":"start","partial":message}),json!({"type":"text_start","contentIndex":0,"partial":message}),json!({"type":"text_delta","contentIndex":0,"delta":"custom provider response","partial":message}),json!({"type":"text_end","contentIndex":0,"content":"custom provider response","partial":message}),json!({"type":"done","reason":"stop","message":message})]{stream.push(event)}
  Ok(stream)
 });
    // The producer thread is cancelled in the middle of a burst: it pushes start and three text_delta events, waits until its request is cancelled, and goes on pushing.
    extension.register_provider_stream("cancel-burst-provider",json!({"api":"cancel-burst-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,_request,_options|{
  let mut message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":""}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  let producer=stream.clone();
  let ctx=ctx.clone();
  std::thread::spawn(move||{
   producer.push(json!({"type":"start","partial":message.clone()}));
   for i in 0..6usize{
    if i==3{let deadline=std::time::Instant::now()+std::time::Duration::from_secs(10);while !ctx.is_cancelled()&&std::time::Instant::now()<deadline{std::thread::sleep(std::time::Duration::from_millis(5));}}
    message["content"][0]["text"]=json!("x".repeat(i+1));
    producer.push(json!({"type":"text_delta","contentIndex":0,"delta":"x","partial":message.clone()}));
   }
   producer.push(json!({"type":"done","reason":"stop","message":message}));
  });
  Ok(stream)
 });
    // The producer answers the cancel of its request by ending its stream with a result: it pushes start and three text_delta events, waits until its request is cancelled, and calls end with the message, pushing nothing more.
    extension.register_provider_stream("cancel-end-provider",json!({"api":"cancel-end-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,_request,_options|{
  let mut message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":""}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  let producer=stream.clone();
  let ctx=ctx.clone();
  std::thread::spawn(move||{
   producer.push(json!({"type":"start","partial":message.clone()}));
   for i in 0..3usize{
    message["content"][0]["text"]=json!("x".repeat(i+1));
    producer.push(json!({"type":"text_delta","contentIndex":0,"delta":"x","partial":message.clone()}));
   }
   let deadline=std::time::Instant::now()+std::time::Duration::from_secs(10);while !ctx.is_cancelled()&&std::time::Instant::now()<deadline{std::thread::sleep(std::time::Duration::from_millis(5));}
   producer.end(message);
  });
  Ok(stream)
 });
    // The producer answers the abort of its request with an error event that holds its partial message: it pushes start and three text_delta events, waits until its request's signal aborts, and pushes the error event (reason aborted) whose message is the partial with stopReason aborted and its errorMessage.
    extension.register_provider_stream("cancel-error-provider",json!({"api":"cancel-error-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,_request,_options|{
  let mut message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":""}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  let producer=stream.clone();
  let ctx=ctx.clone();
  std::thread::spawn(move||{
   producer.push(json!({"type":"start","partial":message.clone()}));
   for i in 0..3usize{
    message["content"][0]["text"]=json!("x".repeat(i+1));
    producer.push(json!({"type":"text_delta","contentIndex":0,"delta":"x","partial":message.clone()}));
   }
   let deadline=std::time::Instant::now()+std::time::Duration::from_secs(10);while !ctx.is_cancelled()&&std::time::Instant::now()<deadline{std::thread::sleep(std::time::Duration::from_millis(5));}
   message["stopReason"]=json!("aborted");
   message["errorMessage"]=json!("the author saw the abort");
   producer.push(json!({"type":"error","reason":"aborted","error":message}));
  });
  Ok(stream)
 });
    // streamSimple waits until its request's signal aborts and fails, showing nothing.
    extension.register_provider_stream("cancel-throw-provider",json!({"api":"cancel-throw-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,_model,_request,_options|{
  let deadline=std::time::Instant::now()+std::time::Duration::from_secs(10);while !ctx.is_cancelled()&&std::time::Instant::now()<deadline{std::thread::sleep(std::time::Duration::from_millis(5));}
  Err("the author's stream threw at the abort".into())
 });
    // The author never reacts to the abort of its request: it pushes start and one text_delta whose text is the id of its process, waits until its request's signal aborts, and returns: it pushes nothing more and never ends its stream.
    extension.register_provider_stream("cancel-never-provider",json!({"api":"cancel-never-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|ctx,model,_request,_options|{
  let mut message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":""}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  let producer=stream.clone();
  let ctx=ctx.clone();
  std::thread::spawn(move||{
   producer.push(json!({"type":"start","partial":message.clone()}));
   let pid=std::process::id().to_string();
   message["content"][0]["text"]=json!(pid.clone());
   producer.push(json!({"type":"text_delta","contentIndex":0,"delta":pid,"partial":message.clone()}));
   while !ctx.is_cancelled(){std::thread::sleep(std::time::Duration::from_millis(5));}
  });
  Ok(stream)
 });
    // A producer thread appends to one message and pushes a copy of it, without yielding between the two: an event shows the message as it was at the push.
    extension.register_provider_stream("snapshot-provider",json!({"api":"snapshot-extension-api","baseUrl":"https://extension.invalid","apiKey":"extension-key","models":[{"id":"faux","name":"Faux","reasoning":false,"input":["text"],"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0},"contextWindow":128000,"maxTokens":4096}]}),|_ctx,model,_request,_options|{
  let mut message=json!({"role":"assistant","api":model["api"],"provider":model["provider"],"model":model["id"],"content":[{"type":"text","text":""}],"stopReason":"stop","timestamp":1,"usage":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"totalTokens":0,"cost":{"input":0,"output":0,"cacheRead":0,"cacheWrite":0,"total":0}}});
  let stream=Arc::new(ModelEventStream::new());
  let producer=stream.clone();
  std::thread::spawn(move||{
   producer.push(json!({"type":"start","partial":message.clone()}));
   let mut text=String::new();
   for _ in 0..100{text.push('x');message["content"][0]["text"]=json!(text);producer.push(json!({"type":"text_delta","contentIndex":0,"delta":"x","partial":message.clone()}))}
   message["content"][0]["text"]=json!(format!("{text}-AFTER"));
   producer.push(json!({"type":"done","reason":"stop","message":message}));
  });
  Ok(stream)
 });
    extension
}
