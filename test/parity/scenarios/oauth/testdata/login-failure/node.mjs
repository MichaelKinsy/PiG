// Two provider objects whose OAuth login asks for a pasted sign-in code and then fails in its token exchange, for Pi and for PiG's Node runtime alike.
// login-exchange-400: the exchange endpoint (a server this extension starts on a loopback port) answers 400 with a JSON body.
// login-refused: the exchange endpoint is a closed loopback port, so the request is refused.
import http from "node:http";

const server = http.createServer((_request, response) => {
  response.writeHead(400, { "content-type": "application/json" });
  response.end('{"error":"invalid_grant"}');
});
server.unref();
const listening = new Promise((resolve) => server.listen(0, "127.0.0.1", () => resolve(server.address().port)));

const model = (provider) => ({ id: "failing-model", name: "Failing Model", provider, api: "openai-completions", baseUrl: "http://127.0.0.1:9", reasoning: false, input: ["text"], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4000, maxTokens: 100 });

const register = (pi, id, name, exchange) => {
  pi.registerProvider({
    id,
    name,
    auth: {
      oauth: {
        name: `${name} account`,
        login: async (interaction) => {
          const code = await interaction.prompt({ type: "text", message: "Paste the sign-in code" });
          const response = await exchange(code);
          const body = await response.text();
          if (!response.ok) throw new Error(`Token exchange failed: ${response.status} ${body}`);
          return { type: "oauth", access: code, refresh: "refresh", expires: 4102444800000 };
        },
        refresh: async (credential) => credential,
        toAuth: async (credential) => ({ apiKey: credential.access }),
      },
    },
    getModels: () => [model(id)],
    stream() { throw new Error("unused"); },
    streamSimple() { throw new Error("unused"); },
  });
};

export default (pi) => {
  register(pi, "login-exchange-400", "Exchange Four Hundred", async (code) => fetch(`http://127.0.0.1:${await listening}/token`, { method: "POST", body: code }));
  register(pi, "login-refused", "Refused Endpoint", async (code) => fetch("http://127.0.0.1:1/token", { method: "POST", body: code }));
};
