export async function disposeLoadedFacets(loaded) {
    const results = await Promise.allSettled(loaded.map((entry) => entry.dispose()));
    return results.flatMap((result) => (result.status === "rejected" ? [result.reason] : []));
}
//# sourceMappingURL=loader.js.map