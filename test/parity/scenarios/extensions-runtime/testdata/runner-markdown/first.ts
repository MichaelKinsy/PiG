export default function(pi) {
  pi.registerMarkdownTransformer((markdown) => `A(${markdown})`);
}
