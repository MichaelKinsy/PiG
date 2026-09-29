export default function(pi) {
	pi.registerMarkdownTransformer((markdown) => {
		return markdown;
	});
	pi.registerMessageRenderer("my-custom-type", (message, options, theme) => {
		return null; // Use default rendering
	});
	pi.registerEntryRenderer("my-entry-type", (entry, options, theme) => {
		return null;
	});
}
