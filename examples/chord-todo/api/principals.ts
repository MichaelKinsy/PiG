// The todo app's principals, as the plugins' types know them: its server and list workers, its owner, and the users who signed in
// with a name. The Go app (`main.go`) makes the same JSON values.

/** Who someone is in the todo app. */
export type TodoPrincipal =
	| { readonly kind: "server" }
	| { readonly kind: "worker"; readonly list: string }
	| { readonly kind: "owner" }
	| { readonly kind: "user"; readonly id: string; readonly name: string };
