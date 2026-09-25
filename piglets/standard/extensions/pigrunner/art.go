package pigrunner

import (
	"image/color"

	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
)

// pigPaletteForVariant returns the login mascot's colors for the selected
// sprite, so the runner pig matches the login pig exactly, including sprites
// that add their own symbols (the Sheriff's hat).
func pigPaletteForVariant(v standardlogin.Variant) map[byte]color.RGBA {
	return standardlogin.MascotPalette(v)
}

// ---------------------------------------------------------------------------
// Runner pig — 14 wide × 12 tall
// ---------------------------------------------------------------------------
//
// Adapted directly from standardlogin.pigMascot by taking the inner 14 columns
// (cols 1–14 of the 16-wide login grid) and removing the blank face-gap row
// to fit 12 rows. All login features are preserved:
//
//   EARS   4px-wide rounded ears with inner 'e' fill (rows 1–3 of content)
//   EYES   2×2 kawaii eyes: top row all-'W', bottom row W+K (pupils inward)
//          Both rows are perfectly symmetric — no trailing 'p' on the right.
//   BLUSH  2px-wide 'bb', combined with snout top on one row (as in login)
//   SNOUT  Oval: 4px at top/bottom, widens to 6px at the nostril row
//   SMILE  'KK' at cols 6–7 of the chin row — center exactly 6.5, which is
//          the geometric centre of a 14-wide grid. Positioned between the
//          nostril 'K' columns (5 and 8) so the features never merge visually.
//
// Animation: pigRunA has an empty row at the top (pig sits one pixel lower),
// pigRunB has it at the bottom (pig sits one pixel higher). Alternate at the
// stride cadence for a gentle running bob.

var pigRunA = []string{
	"..............", // 0  padding — bob low
	"..OOOO..OOOO..", // 1  round ears (4px wide each)
	"..OeeO..OeeO..", // 2  inner ear fill
	".OeePPPPPPeeO.", // 3  ear base blends into head
	"OPPPpPPPPpPPPO", // 4  forehead — dual symmetric highlight columns
	"OPPWWPPPPWWPPO", // 5  eye whites — fully symmetric (W at cols 3–4, 9–10)
	"OPPWKPPPPKWPPO", // 6  pupils inward + sparkle — symmetric
	"OPbbPssssPbbPO", // 7  blush cheeks + snout top (combined row)
	"OPPPsKssKsPPPO", // 8  snout (6px wide) + K nostrils at cols 5, 8
	"OPPPPssssPPPPO", // 9  snout bottom (4px, symmetric taper)
	".OPPPPPPPPPPO.", // 10 chin + smile: KK at cols 6–7, center = 6.5 ✓
	"..OOOOOOOOOO..", // 11 bottom outline
}

var pigRunB = []string{
	"..OOOO..OOOO..", // 0  round ears — bob high
	"..OeeO..OeeO..", // 1  inner ear fill
	".OeePPPPPPeeO.", // 2  ear base
	"OPPPpPPPPpPPPO", // 3  forehead
	"OPPWWPPPPWWPPO", // 4  eye whites
	"OPPWKPPPPKWPPO", // 5  pupils + sparkle
	"OPbbPssssPbbPO", // 6  blush + snout top
	"OPPPsKssKsPPPO", // 7  nostrils
	"OPPPPssssPPPPO", // 8  snout bottom
	".OPPPPPPPPPPO.", // 9  chin + smile
	"..OOOOOOOOOO..", // 10 bottom outline
	"..............", // 11 padding — bob high
}

// ---------------------------------------------------------------------------
// Duck — 14 wide × 7 tall
// ---------------------------------------------------------------------------
//
// Squished version of the runner pig for low-clearance obstacles. Ears are
// merged into the flat top outline. With no room for a 2-row eye box, each
// eye is a single WK pair (sparkle W on the outer edge, inward pupil K):
//
//   left eye:   W at col 4, K at col 5  → pupil faces right (inward)
//   right eye:  K at col 8, W at col 9  → pupil faces left  (inward)
//   each eye center: 4.5 and 8.5 → overall eye centre = 6.5 ✓
//
// Smile uses KK at cols 6–7 (same as running pig) which sits between the
// nostril K columns (5, 8) so the two features stay distinct even though
// there is no snout-bottom separator row in the squished layout.

var pigDuck = []string{
	"..............", // 0  padding (height-aligns with run sprites)
	"..OOOOOOOOOO..", // 1  flat top outline (ears hidden in squat)
	".OPPWKPPKWPPO.", // 2  eyes: WK left + KW right, pupils face inward
	"OPbbPssssPbbPO", // 3  blush cheeks + snout top
	"OPPPsKssKsPPPO", // 4  snout (6px) + K nostrils at cols 5, 8
	".OPPPPPPPPPPO.", // 5  chin: KK at cols 6–7, center = 6.5 ✓
	"..OOOOOOOOOO..", // 6  flat bottom outline
}

func runnerSpritesForVariant(v standardlogin.Variant) (runA, runB, duck []string) {
	if len(v.Sprite) == 0 {
		return pigRunA, pigRunB, pigDuck
	}
	runA = cropLoginSpriteForRunner(v.Sprite, 1)
	runB = cropLoginSpriteForRunner(v.Sprite, 0)
	if len(runA) == 0 || len(runB) == 0 {
		return pigRunA, pigRunB, pigDuck
	}
	return runA, runB, pigDuck
}

func cropLoginSpriteForRunner(sprite []string, startRow int) []string {
	const runnerRows = 12
	if startRow < 0 || len(sprite) < startRow+runnerRows {
		return nil
	}
	out := make([]string, 0, runnerRows)
	for _, row := range sprite[startRow : startRow+runnerRows] {
		if len(row) < 16 {
			return nil
		}
		out = append(out, row[1:15])
	}
	return out
}

// ---------------------------------------------------------------------------
// Hazard palette
// ---------------------------------------------------------------------------

var hazardPalette = map[byte]color.RGBA{
	'.': {0, 0, 0, 0},
	'O': {0x0E, 0x0B, 0x14, 0xFF}, // deep near-black outline
	'C': {0xE0, 0x8F, 0x43, 0xFF}, // golden-brown baked crust
	'c': {0xB8, 0x5F, 0x2E, 0xFF}, // crust shadow / crimped-edge shade
	'f': {0xD9, 0x2E, 0x4C, 0xFF}, // red berry filling
	'W': {0xFF, 0xF1, 0xBF, 0xFF}, // pale inner-crust highlight (cream)
	'F': {0xFF, 0xD2, 0x7A, 0xFF}, // custard / egg-wash sheen (reserved)
	'w': {0x69, 0xB8, 0xE5, 0xFF}, // wing blue
}

// ---------------------------------------------------------------------------
// Pie obstacle sprites — 10 wide × 6 tall
// ---------------------------------------------------------------------------
//
// Redesigned from the original 10×5 to 10×6 to fit a proper silhouette:
//
//   row 0  domed crust top — the "arch" that makes it read as PIE not BLOB
//   row 1  pale inner surface of the crust lid (cream 'W')
//   row 2  berry filling begins, outline ('O') and crust walls ('C') visible
//   row 3  thick filling layer — the richest red, widest row of 'f'
//   row 4  solid bottom crust — distinguishes pie from a puddle
//   row 5  ground-shadow outline
//
// If your collision box was tuned to height 5, bump it to 6 here.
//
// Frames A / B animate the crust crimp at ~4 Hz:
//   A — plain golden dome (uniform 'C' top)
//   B — crimped dome ('c' shadow pixels at the fluted outer edges)
// The wobble reads as a freshly-baked, slightly wobbly pie.

var groundPieA = []string{
	"...CCCC...", // 0  domed golden crust
	"..CWWWWC..", // 1  pale cream inner surface
	".OCffffCO.", // 2  berry filling + crust walls
	"OCffffffCO", // 3  thick berry filling
	"OCCCCCCCCO", // 4  solid bottom crust
	".OOOOOOOO.", // 5  ground-shadow outline
}

var groundPieB = []string{
	"..cCCCCc..", // 0  crimped crust: shadow 'c' at the fluted rim edges
	"..CWWWWC..", // 1  same pale inner arch
	".OCffffCO.", // 2  filling
	"OCffffffCO", // 3  filling
	"OCCCCCCCCO", // 4  bottom crust
	".OOOOOOOO.", // 5  outline
}

// Flying pie — 10 wide × 6 tall.
// Wing pixels ('w', sky-blue) extend from rows 0–1 only; the pie body
// below is identical to the ground variant. The staggered wing positions
// (tips at 0,9 in row 0; shoulders at 1,8 in row 1) read as a swept-back
// wing shape and also animate naturally when looped against groundPieA/B.

var flyingPie = []string{
	"w..CCCC..w", // 0  wing tips + golden crust dome
	".wCWWWWCw.", // 1  swept-back wings + pale crust arch
	".OCffffCO.", // 2  filling — wings end, pie body only from here
	"OCffffffCO", // 3  thick filling
	"OCCCCCCCCO", // 4  bottom crust
	".OOOOOOOO.", // 5  outline
}
