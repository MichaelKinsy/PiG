import { replaceControlCharacters } from "../../../pi-tui.mjs";
import { APP_NAME } from "../config.js";
import { CredentialSynchronizationError } from "../core/model-runtime.js";
import { findMatchingProviders } from "../core/provider-matching.js";
const AUTH_LOGOUT_USAGE = `${APP_NAME} auth logout <provider>`;
/**
 * Run `pi auth logout <provider>`: remove the credential `pi auth login` or /login stored, and return the exit code.
 * Environment variables and models.json config stay, as with /logout.
 */
export async function runAuthLogout(args, options) {
    const { modelRuntime, terminal, signal } = options;
    const [providerRef, ...rest] = args;
    if (providerRef === undefined || providerRef.startsWith("-") || rest.length > 0) {
        terminal.error(`Usage: ${AUTH_LOGOUT_USAGE}`);
        return 1;
    }
    let stored;
    try {
        stored = await modelRuntime.listCredentials({ signal });
    }
    catch (error) {
        terminal.error(`Could not read stored credentials: ${error instanceof Error ? error.message : String(error)}`);
        return 1;
    }
    const provider = findMatchingProviders(modelRuntime.getProviders(), providerRef)[0];
    // Providers registered by extensions are not loaded here, so their stored credentials match by id alone.
    const credential = stored.find((candidate) => candidate.providerId === (provider?.id ?? providerRef));
    if (!provider && !credential) {
        terminal.error(`Unknown provider "${replaceControlCharacters(providerRef)}".`);
        const storedIds = stored.map((credential) => credential.providerId).sort();
        terminal.error(`Stored credentials: ${storedIds.join(", ") || "none"}`);
        return 1;
    }
    const providerName = provider?.name ?? providerRef;
    if (!credential) {
        terminal.write(`No stored credentials for ${providerName}. Environment variables and models.json config are unchanged.`);
        return 0;
    }
    try {
        await modelRuntime.logout(credential.providerId, { signal });
    }
    catch (error) {
        const message = error instanceof Error ? error.message : String(error);
        if (error instanceof CredentialSynchronizationError) {
            terminal.error(`Credentials removed for ${providerName}, but local model state could not be synchronized: ${message}`);
        }
        else {
            terminal.error(`Logout failed: ${message}`);
        }
        return 1;
    }
    terminal.write(credential.type === "oauth"
        ? `Logged out of ${providerName}.`
        : `Removed stored API key for ${providerName}. Environment variables and models.json config are unchanged.`);
    return 0;
}
//# sourceMappingURL=auth-logout.js.map