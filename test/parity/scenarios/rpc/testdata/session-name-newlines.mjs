export default function (pi) {
	pi.registerCommand("newline-name", {
		description: "Set the regression Session name",
		handler: async () => { pi.setSessionName("from\nextension"); },
	});
}
