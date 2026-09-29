export default function(pi) {
  pi.registerMarkdownTransformer((markdown) => `B(${markdown})`);
}
