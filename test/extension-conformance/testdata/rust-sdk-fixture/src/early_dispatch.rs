//! The Rust extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched, or writes its tag to a file when PIG_TEST_PURE_LISTENER names a directory.
use pig_sdk::Extension;

fn main() {
    let ext = Extension::new("early-dispatch");
    let pure = std::env::var("PIG_TEST_PURE_LISTENER").ok();
    ext.events()
        .on("early:ch", move |ctx, data| match &pure {
            Some(dir) => std::fs::write(
                std::path::Path::new(dir).join(data["seq"].to_string()),
                data["tag"].as_str().unwrap_or_default(),
            )
            .map_err(|err| err.to_string()),
            None => ctx
                .events()
                .emit("early:echo", data.clone())
                .map_err(|err| err.to_string()),
        })
        .unwrap();
    if std::env::var("PIG_TEST_FACTORY_EMIT").is_ok() {
        ext.events()
            .emit("early:factory", serde_json::json!(1))
            .unwrap();
    }
    ext.run().unwrap();
}
