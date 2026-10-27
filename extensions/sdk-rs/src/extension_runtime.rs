//! Pi's `createExtensionRuntime()` result (core/extensions/loader.ts): the runtime state every extension of a load registers against, which an `ExtensionRunner` over those extensions binds its actions into. The host owns the object. A `DefaultResourceLoader` given it in `DefaultResourceLoaderOptions::runtime` loads its extensions against it, and `DefaultResourceLoader::get_extensions` returns the runtime of the load as `LoadExtensionsResult::runtime`.
//!
//! Pi's runtime is a record of functions and Maps; this holds its data members: the flag values, the provider, native provider and virtual model registrations queued before a runner bound, the registered MCP servers and the stale state. The action functions (sendMessage and the rest) are the runner's, bound by `ExtensionRunner::bind_core`.

use crate::context::Context;
use crate::graph::GraphObject;
use serde_json::{json, Value};
use std::io;

/// The runtime of Pi's `createExtensionRuntime()`. Dropping it releases the host's reference; a loader or runner given it keeps its own.
pub struct ExtensionRuntime {
    object: GraphObject,
}

/// `createExtensionRuntime()`.
pub fn create_extension_runtime(context: &Context) -> io::Result<ExtensionRuntime> {
    Ok(ExtensionRuntime { object: GraphObject::construct(context, "ExtensionRuntime", "create", vec![])? })
}

impl ExtensionRuntime {
    /// The runtime of the extensions a loader loaded: `loader.getExtensions().runtime`.
    pub(crate) fn of_loader(context: &Context, loader_handle: &str) -> io::Result<ExtensionRuntime> {
        Ok(ExtensionRuntime { object: GraphObject::construct(context, "ExtensionRuntime", "of", vec![json!(loader_handle)])? })
    }

    /// The host's name for the runtime, which a loader option and `ExtensionRunner::new` put on the wire.
    pub fn handle(&self) -> &str {
        self.object.handle()
    }

    /// `new Map(runtime.flagValues)` as an object: the value of each flag that has one, as the registrations' defaults and later `set_flag_value` calls left them. The host lists them by name.
    pub fn get_flag_values(&self) -> io::Result<Value> {
        self.object.call("getFlagValues", vec![])
    }

    /// `runtime.flagValues.set(name, value)`: `value` is a boolean or a string.
    pub fn set_flag_value(&self, name: &str, value: Value) -> io::Result<()> {
        self.object.call("setFlagValue", vec![json!(name), value]).map(|_| ())
    }

    /// `runtime.pendingProviderRegistrations`: `[{name, config, extensionPath}]` in registration order, the providers an extension registered while loading, which the runner's `bind_core` applies.
    pub fn pending_provider_registrations(&self) -> io::Result<Value> {
        self.object.call("getPendingProviderRegistrations", vec![])
    }

    /// `runtime.pendingNativeProviderRegistrations`: `[{provider: {id, name}, extensionPath}]` in registration order.
    pub fn pending_native_provider_registrations(&self) -> io::Result<Value> {
        self.object.call("getPendingNativeProviderRegistrations", vec![])
    }

    /// `runtime.pendingVirtualModelRegistrations`: `[{definition, extensionPath}]` in registration order, without the route function.
    pub fn pending_virtual_model_registrations(&self) -> io::Result<Value> {
        self.object.call("getPendingVirtualModelRegistrations", vec![])
    }

    /// `runtime.mcpServers.list()`: the MCP servers extensions registered, `[{name, config, extensionPath}]` in registration order.
    pub fn mcp_servers(&self) -> io::Result<Value> {
        self.object.call("getMcpServers", vec![])
    }

    /// `runtime.assertActive()`: it fails with the stale message once the runtime was invalidated.
    pub fn assert_active(&self) -> io::Result<()> {
        self.object.call("assertActive", vec![]).map(|_| ())
    }

    /// `runtime.invalidate(message?)`: later uses of the runtime fail with `message`, or Pi's default one when it is `None`. A second call keeps the first message.
    pub fn invalidate(&self, message: Option<&str>) -> io::Result<()> {
        self.object.call("invalidate", vec![json!(message)]).map(|_| ())
    }
}
