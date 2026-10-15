import { createServer } from "node:http";

// Registers an OAuth-protected MCP server whose authorization server never answers: the MCP endpoint
// rejects every request with a 401 that names the protected resource metadata, the metadata names this
// origin as the authorization server, and the authorization server metadata request is accepted and
// left open. A sign-in therefore stays at "Contacting the authorization server…" until it is cancelled.
export default async function (pi: any) {
  let origin = "";
  const server = createServer((request, response) => {
    const path = new URL(request.url ?? "/", origin).pathname;
    request.resume();
    if (path === "/mcp") {
      response.writeHead(request.method === "POST" ? 401 : 405, {
        "www-authenticate": `Bearer resource_metadata="${origin}/.well-known/oauth-protected-resource/mcp"`,
      });
      response.end();
    } else if (path === "/.well-known/oauth-protected-resource/mcp") {
      response.writeHead(200, { "content-type": "application/json" });
      response.end(JSON.stringify({ resource: `${origin}/mcp`, authorization_servers: [origin] }));
    }
    // Every other path, including the authorization server metadata, is never answered.
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  server.unref();
  const address = server.address();
  origin = `http://127.0.0.1:${typeof address === "object" && address ? address.port : 0}`;
  pi.registerMcpServer("issues", { url: `${origin}/mcp` });
  pi.on("session_shutdown", () => {
    server.closeAllConnections();
    server.close();
  });
}
