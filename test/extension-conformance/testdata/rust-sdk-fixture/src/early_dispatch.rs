//! The Rust extension of the pre-ready dispatch row (early_dispatch_conformance_test.go): one listener that echoes the payload it was dispatched.
use pig_sdk::Extension;

fn main() {
    let ext = Extension::new("early-dispatch");
    ext.events()
        .on("early:ch", |ctx, data| {
            ctx.events()
                .emit("early:echo", data.clone())
                .map_err(|err| err.to_string())
        })
        .unwrap();
    ext.run().unwrap();
}
