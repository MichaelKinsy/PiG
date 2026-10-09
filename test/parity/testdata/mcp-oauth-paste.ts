import { createHash } from "node:crypto";
import { createServer } from "node:http";

// Registers an OAuth-protected MCP server whose authorization server registers the client and then waits for the
// browser: a sign-in reaches the screen that shows the authorization URL and asks for the redirect URL. The MCP
// endpoint rejects every request with a 401 that names the protected resource metadata, and the metadata names this
// origin as the authorization server. Nothing here follows the authorization URL.
export default async function (pi: any) {
  let origin = "";
  // The loopback callback page the client served for a redirect with a state it never issued, fetched when the client
  // registers (its callback server already listens then). `/callback-page` reports it.
  let callbackPage = "(no registration yet)";
  const json = (response: any, status: number, body: unknown) => {
    response.writeHead(status, { "content-type": "application/json" });
    response.end(JSON.stringify(body));
  };
  const server = createServer((request, response) => {
    const path = new URL(request.url ?? "/", origin).pathname;
    const chunks: Buffer[] = [];
    request.on("data", (chunk) => chunks.push(chunk));
    request.on("end", () => {
      if (path === "/mcp") {
        response.writeHead(request.method === "POST" ? 401 : 405, {
          "www-authenticate": `Bearer resource_metadata="${origin}/.well-known/oauth-protected-resource/mcp"`,
        });
        response.end();
      } else if (path === "/.well-known/oauth-protected-resource/mcp") {
        json(response, 200, { resource: `${origin}/mcp`, authorization_servers: [origin] });
      } else if (path === "/.well-known/oauth-authorization-server") {
        json(response, 200, {
          issuer: origin,
          authorization_endpoint: `${origin}/authorize`,
          token_endpoint: `${origin}/token`,
          registration_endpoint: `${origin}/register`,
          response_types_supported: ["code"],
          code_challenge_methods_supported: ["S256"],
          token_endpoint_auth_methods_supported: ["none"],
        });
      } else if (path === "/register") {
        const metadata = JSON.parse(Buffer.concat(chunks).toString() || "{}");
        json(response, 201, { ...metadata, client_id: "client-1" });
        const redirect = metadata.redirect_uris?.[0];
        if (redirect) {
          void fetch(`${redirect}?code=unused&state=never-issued`).then(async (page) => {
            const html = await page.text();
            const text = html.replace(/<style[\s\S]*?<\/style>/g, "").replace(/<[^>]+>/g, " ").replace(/\s+/g, " ").trim();
            callbackPage = `status=${page.status} type=${page.headers.get("content-type")} bytes=${html.length} sha256=${createHash("sha256").update(html).digest("hex")}\ntext=${text}`;
          });
        }
      }
      // Every other path is never answered.
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  server.unref();
  const address = server.address();
  origin = `http://127.0.0.1:${typeof address === "object" && address ? address.port : 0}`;
  pi.registerMcpServer("issues", { url: `${origin}/mcp` });
  pi.registerCommand("callback-page", {
    description: "Report the callback page the sign-in served",
    handler: async (_args: string, ctx: any) => {
      ctx.ui.notify(`callback-page-begin\n${callbackPage}\ncallback-page-end`, "info");
    },
  });
  pi.on("session_shutdown", () => {
    server.closeAllConnections();
    server.close();
  });
}
