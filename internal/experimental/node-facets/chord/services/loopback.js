/** Connects a provider to a binding without changing remote service semantics. */
export function createLoopbackServiceTransport(provider) {
    return {
        invoke: (call, context) => provider.invoke(call, context),
        subscribe: async (serviceId, mode, listener) => {
            const subscription = provider.subscribe(serviceId, mode, listener);
            return {
                snapshot: subscription.snapshot,
                activate: () => subscription.activate(),
                close: () => subscription.close(),
            };
        },
    };
}
//# sourceMappingURL=loopback.js.map