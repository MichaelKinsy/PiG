export default function () {
  const error = new SyntaxError("PT_MULTILINE_FACTORY_FAILURE");
  // Node's TypeScript parser places a location and source excerpt before the
  // named error line. Keep that layout while using a valid portable module.
  error.stack = "/extension.ts:2\n  foo(, )\n      ^\n\nSyntaxError: PT_MULTILINE_FACTORY_FAILURE\n    at parseTypeScript (node:internal:72:36)";
  throw error;
}
