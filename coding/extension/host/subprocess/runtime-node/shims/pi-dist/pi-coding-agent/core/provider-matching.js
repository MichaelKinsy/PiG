/** Match provider IDs and display names exactly, ignoring case and surrounding whitespace in the reference. */
export function findMatchingProviders(providers, providerRef) {
    const normalized = providerRef.trim().toLowerCase();
    if (!normalized)
        return [];
    return providers.filter((provider) => provider.id.toLowerCase() === normalized || provider.name.toLowerCase() === normalized);
}
//# sourceMappingURL=provider-matching.js.map