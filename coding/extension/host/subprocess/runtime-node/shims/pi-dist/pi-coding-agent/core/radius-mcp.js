import path from "node:path";
import { mcpNamespace } from "../../pi-mcp/index.js";
import { loadMcpConfig } from "../extensions/mcp/config.js";
import { RADIUS_MCP_URL } from "./radius.js";
/**
 * The global mcp.json entry that makes the Radius MCP server use the login of `providerId`, adding the server when
 * missing, or undefined when a global server already uses that login.
 */
export function planRadiusMcpServer(providerId, agentDir, cwd) {
    const normalizeUrl = (url) => url.replace(/\/+$/u, "");
    const { servers } = loadMcpConfig({ agentDir, cwd, projectTrusted: false });
    const existing = servers.find((server) => "url" in server.config && normalizeUrl(server.config.url) === normalizeUrl(RADIUS_MCP_URL));
    if (existing && "url" in existing.config && existing.config.auth?.provider === providerId)
        return undefined;
    let name = existing?.name ?? "radius";
    if (!existing) {
        const namespaces = new Set(servers.map((server) => mcpNamespace(server.name)));
        for (let suffix = 1; namespaces.has(mcpNamespace(name)); suffix++) {
            name = suffix === 1 ? "radius-mcp" : `radius-mcp-${suffix}`;
        }
    }
    const config = existing && "url" in existing.config
        ? { ...existing.config, auth: { provider: providerId } }
        : { url: RADIUS_MCP_URL, auth: { provider: providerId } };
    // `auth` replaces the MCP OAuth sign-in.
    delete config.oauth;
    return { mcpPath: path.join(agentDir, "mcp.json"), name, config };
}
//# sourceMappingURL=radius-mcp.js.map