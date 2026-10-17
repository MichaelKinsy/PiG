// Renders slide HTML files to 2000x1125 PNGs with headless Chromium through Playwright, and reports layout faults:
// fonts that did not load, elements outside the slide, clipped boxes, children outside their panel or footer, slide
// content that runs into the honesty footer, and chart labels that overlap each other.
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { pathToFileURL } from "node:url";
import { chromium } from "playwright-core";
import { HEIGHT, WIDTH } from "./slides.mjs";

export const SCALE = 1.25; // 1600x900 layout -> 2000x1125 pixels

/** CHROME, else the newest Playwright headless shell or Chromium in the cache, else Chrome or Chromium on PATH. */
export function findChrome() {
	if (process.env.CHROME) return process.env.CHROME;
	const cache = process.env.PLAYWRIGHT_BROWSERS_PATH ?? join(homedir(), ".cache", "ms-playwright");
	if (existsSync(cache)) {
		const dirs = readdirSync(cache)
			.map((d) => /^(chromium_headless_shell|chromium)-(\d+)$/.exec(d))
			.filter(Boolean)
			.sort((a, b) => Number(b[2]) - Number(a[2]) || (a[1] === "chromium_headless_shell" ? -1 : 1));
		for (const [d, kind] of dirs) {
			for (const exe of kind === "chromium" ? ["chrome-linux64/chrome", "chrome-linux/chrome"] : ["chrome-headless-shell-linux64/chrome-headless-shell", "chrome-linux/headless_shell"]) {
				const p = join(cache, d, exe);
				if (existsSync(p)) return p;
			}
		}
	}
	for (const bin of ["chromium", "chromium-browser", "google-chrome"]) {
		try {
			return execFileSync("sh", ["-c", `command -v ${bin}`], { encoding: "utf8" }).trim();
		} catch {}
	}
	throw new Error("no Chromium found: set CHROME, or install one with `npx playwright-core install chromium-headless-shell`");
}

/** Screenshots each html file to png; returns [{file, problems}]. */
export async function shoot(files, outDir) {
	const executablePath = findChrome();
	const browser = await chromium.launch({ executablePath });
	const out = [];
	try {
		const page = await browser.newPage({ viewport: { width: WIDTH, height: HEIGHT }, deviceScaleFactor: SCALE });
		for (const file of files) {
			await page.goto(pathToFileURL(file).href);
			// fonts.ready can resolve before a face has started loading (status "unloaded"); load every face explicitly,
			// so a failed load is reported below as a fault rather than racing the check.
			await page.evaluate(() => Promise.allSettled([...document.fonts].map((f) => f.load())).then(() => document.fonts.ready));
			const problems = await page.evaluate(
				([w, h]) => {
					const faults = new Set();
					const box = (el) => el.getBoundingClientRect();
					const tag = (el) => `<${el.tagName.toLowerCase()}${el.getAttribute("class") ? ` class="${el.getAttribute("class")}"` : ""}>`;
					for (const f of document.fonts) if (f.status !== "loaded") faults.add(`font ${f.family} is ${f.status}`);
					const measured = [...document.body.querySelectorAll("*")].filter((el) => !el.closest(".wm"));
					for (const el of measured) {
						const b = box(el);
						if (b.width + b.height === 0) continue;
						if (b.right > w + 1 || b.bottom > h + 1) faults.add(`${tag(el)} ends at ${Math.round(b.right)}x${Math.round(b.bottom)}, outside ${w}x${h}`);
						const hidden = getComputedStyle(el).overflow !== "visible";
						if (hidden && (el.scrollHeight - el.clientHeight > 2 || el.scrollWidth - el.clientWidth > 2)) faults.add(`${tag(el)} clips its content`);
					}
					for (const outer of document.querySelectorAll(".panel, .honest")) {
						const o = box(outer);
						const out = [...outer.querySelectorAll("*")].find((c) => {
							const b = box(c);
							return b.width > 0 && (b.right > o.right + 1 || b.bottom > o.bottom + 1);
						});
						if (out) faults.add(`${tag(out)} "${(out.textContent ?? "").slice(0, 50)}" leaves ${tag(outer)}`);
					}
					for (const svg of document.querySelectorAll("svg")) {
						const texts = [...svg.querySelectorAll("text")].map((t) => [t.textContent, box(t)]);
						// The SVG viewport clips its content: a label that leaves the chart (a bar label moved up to clear its
						// neighbour) loses part of its value without any other fault.
						const s = box(svg);
						for (const [a, ra] of texts) if (ra.top < s.top - 1 || ra.left < s.left - 1 || ra.right > s.right + 1 || ra.bottom > s.bottom + 1) faults.add(`chart label "${a}" leaves its chart`);
						for (const [i, [a, ra]] of texts.entries())
							for (const [b, rb] of texts.slice(i + 1))
								if (ra.left < rb.right - 1 && rb.left < ra.right - 1 && ra.top < rb.bottom - 1 && rb.top < ra.bottom - 1) faults.add(`chart labels "${a}" and "${b}" overlap`);
					}
					const foot = document.querySelector(".honest");
					if (foot) {
						const top = box(foot).top;
						for (const el of document.body.children)
							if (!el.matches(".honest, .wm") && box(el).height > 0 && box(el).bottom > top + 1) faults.add(`${tag(el)} runs into the footer`);
					}
					return [...faults].slice(0, 8);
				},
				[WIDTH, HEIGHT],
			);
			const png = join(outDir, file.split("/").pop().replace(/\.html$/, ".png"));
			await page.screenshot({ path: png });
			out.push({ file, png, problems });
		}
	} finally {
		await browser.close();
	}
	return out;
}
