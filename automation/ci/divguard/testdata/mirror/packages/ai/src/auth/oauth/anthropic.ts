// Fixture stand-in: the first callback wins, later ones are ignored.
export function waitForCode(): void {
	let settled = false;
	const finish = () => {
		if (settled) return;
		settled = true;
	};
	finish();
}
