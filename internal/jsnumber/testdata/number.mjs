// Reads {"doubles": [hex bit patterns], "texts": [strings]} on stdin; prints String(x) for each double and Number(text) as bit patterns for each text.
process.stdin.setEncoding("utf8");
let text = "";
for await (const chunk of process.stdin) text += chunk;
const { doubles, texts } = JSON.parse(text);
const view = new DataView(new ArrayBuffer(8));
const bits = (x) => {
	view.setFloat64(0, x);
	return view.getBigUint64(0).toString(16).padStart(16, "0");
};
const fromBits = (hex) => {
	view.setBigUint64(0, BigInt("0x" + hex));
	return view.getFloat64(0);
};
process.stdout.write(
	JSON.stringify({
		strings: doubles.map((hex) => String(fromBits(hex))),
		json: doubles.map((hex) => JSON.stringify(fromBits(hex)) ?? "undefined"),
		numbers: texts.map((s) => {
			const n = Number(s);
			return Number.isNaN(n) ? "nan" : bits(n);
		}),
	}),
);
