/** Copy only the uniform operation scope when constructing a successor leaf. */
export function operationScopeOf(state) {
    return {
        control: state.control,
        settings: state.settings,
        latestAssistantEntryId: state.latestAssistantEntryId,
    };
}
//# sourceMappingURL=types.js.map