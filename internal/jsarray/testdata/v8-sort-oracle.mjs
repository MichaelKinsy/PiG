// Generates v8-sort-oracle.json: Array.prototype.sort orders for highlightAuto-shaped comparators that are not a strict weak order.
// Usage: node v8-sort-oracle.mjs > v8-sort-oracle.json
let seed = 42;
const random = () => (seed = (seed * 1103515245 + 12345) % 2147483648) / 2147483648;
const cases = [];
for (const length of [0, 1, 2, 5, 17, 63, 64, 65, 130, 200, 400]) {
	const items = Array.from({ length }, (_, id) => ({ id, relevance: Math.floor(random() * 4), superset: Math.floor(random() * 6) }));
	const order = [...items]
		.sort((a, b) => {
			if (a.relevance !== b.relevance) return b.relevance - a.relevance;
			if (a.superset === b.id % 6) return 1;
			if (b.superset === a.id % 6) return -1;
			return 0;
		})
		.map((item) => item.id);
	cases.push({ items: items.map((item) => [item.id, item.relevance, item.superset]), order });
}
console.log(JSON.stringify(cases));
