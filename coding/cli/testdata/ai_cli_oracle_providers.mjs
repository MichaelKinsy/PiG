// Stub providers for Pi's packages/ai/src/cli.ts (see ai_cli_oracle_hooks.mjs). Each OAuth provider runs one scripted login and
// returns a credential that carries the answer it was given, so the run shows what the prompt, the notifications and the credential
// store did with it. Go's coding/cli/ai_cli_pi_oracle_test.go registers the same three scripts as Go OAuth providers.
const credential = (answer) => ({ type: "oauth", access: `access:${answer}`, refresh: "refresh-token", expires: 4102444800000 });
const uuid = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

const oauth = (login) => ({ oauth: { login } });

export function builtinProviders() {
	return [
		{
			id: "oracle-api-key",
			name: "Oracle API Key Only",
			auth: { apiKey: {} },
		},
		{
			id: "oracle-text",
			name: "Oracle Text",
			auth: oauth(async (interaction, options) => {
				interaction.notify({ type: "progress", message: "Contacting the stub..." });
				const answer = await interaction.prompt({ type: "text", message: "Enter token", placeholder: "tok-123" });
				interaction.notify({ type: "device_code", userCode: "ABCD-1234", verificationUri: "https://example.test/device" });
				interaction.notify({ type: "info", message: `device id is a UUID: ${uuid.test(options.getDeviceId())}` });
				return credential(answer);
			}),
		},
		{
			id: "oracle-auth-url",
			name: "Oracle Auth URL",
			auth: oauth(async (interaction) => {
				interaction.notify({ type: "auth_url", url: "https://example.test/authorize", instructions: "Approve the request." });
				interaction.notify({ type: "auth_url", url: "https://example.test/second" });
				interaction.notify({ type: "info", message: "waiting" });
				return credential("none");
			}),
		},
		{
			id: "oracle-select",
			name: "Oracle Select",
			auth: oauth(async (interaction) => {
				const answer = await interaction.prompt({
					type: "select",
					message: "Pick an account",
					options: [
						{ id: "alpha", label: "Alpha" },
						{ id: "beta", label: "Beta" },
					],
				});
				return credential(answer);
			}),
		},
	];
}
