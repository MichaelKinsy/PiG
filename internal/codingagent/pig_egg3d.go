package codingagent

// Ports packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts (Model, createModel, arminModel, shuffleSteps, blockOffsets)

import (
	"math"

	"github.com/MichaelKinsy/PiG/coding/piglogin"
)

// egg3dStartScale is the braille dots per pixel when a model without an origin appears, growing from a speck at the center.
// upstream: packages/coding-agent/src/modes/interactive/components/easter-egg-3d.ts:START_SCALE
const egg3dStartScale = 0.1

// egg3dModel is the object an easter egg spins: a bitmap built from one block per foreground pixel, and how it appears.
type egg3dModel struct {
	// columns and rows are the bitmap size in pixels; pixel is a pixel's side in grid units.
	columns, rows int
	pixel         float64
	blocks        []logoBlock
	// cameraDistance is the camera's distance from the model's center, in grid units.
	cameraDistance float64
	// startScale is the braille dots per grid unit at the start, and reach the model's projected reach per unit of scale.
	startScale, reach float64
	// widthShare is the largest share of the screen width the spinning model covers.
	widthShare float64
	// origin reports whether the model lifts off its half-block rendering at the options' logo cells; otherwise it grows
	// out of the center.
	origin bool
	// puzzleMoves is the number of blocks the sliding puzzle moves per step, given a random number from 0 to 1. Nil when the
	// model plays no puzzle.
	puzzleMoves func(random float64) int
}

// centerX and centerY are the model's center in grid units.
func (m *egg3dModel) centerX() float64 { return float64(m.columns) * m.pixel / 2 }
func (m *egg3dModel) centerY() float64 { return float64(m.rows) * m.pixel / 2 }

// pigLogoModel is the header pig the logo click lifts off.
//
// pig divergence (D87): Pi's piLogoModel is its 4x4 three-color logo at a unit per pixel and slides its blocks as a puzzle;
// PiG's is the active sprite's head at half a unit per pixel (pigLogoPixel), and the running pig takes the puzzle's place.
func pigLogoModel(variant piglogin.Variant) egg3dModel {
	return egg3dModel{
		columns:        piglogin.HeadWidth,
		rows:           piglogin.HeadHeight,
		pixel:          pigLogoPixel,
		blocks:         pigLogoBlocks(variant),
		cameraDistance: logoCameraDistance,
		startScale:     logoStartScale,
		reach:          pigLogoRadius * logoReachFactor,
		widthShare:     0.35,
		origin:         true,
	}
}

// pig3dModel is the fullscreen /arminsayshi and /pigsayhi object: Pi's arminModel with the active sprite's head in its
// colors as the bitmap.
//
// pig divergence (D87): Pi's bitmap is Armin's 31x36 portrait in the theme's accent color.
func pig3dModel(variant piglogin.Variant) egg3dModel {
	columns, rows := piglogin.HeadWidth, piglogin.HeadHeight
	blocks := pigLogoBlocks(variant)
	// Far enough that the perspective stays mild, as Pi's camera for Armin's 36 blocks.
	const cameraDistance = 80
	radius := jsHypot(float64(columns)/2, float64(rows)/2, 1+logoDepth/2)
	count := len(blocks)
	return egg3dModel{
		columns:        columns,
		rows:           rows,
		pixel:          1,
		blocks:         blocks,
		cameraDistance: cameraDistance,
		startScale:     egg3dStartScale,
		reach:          radius * (cameraDistance / (cameraDistance - radius)),
		widthShare:     0.45,
		puzzleMoves: func(random float64) int {
			return int(jsRoundFloat(float64(count) * (0.03 + random*0.04)))
		},
	}
}

// egg3dPuzzleMoves are the directions a block can slide, with the in-plane ones twice so they are twice as likely.
var egg3dPuzzleMoves = [...]logoCell3{
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0},
	{1, 0, 0}, {-1, 0, 0}, {0, 1, 0}, {0, -1, 0},
	{0, 0, 1}, {0, 0, -1},
}

// shuffleSteps is the block positions after each step of one shuffle cycle, starting at home. Deterministic per cycle.
func (m *egg3dModel) shuffleSteps(cycle int) [][]logoCell3 {
	columns, rows := m.columns, m.rows
	positions := make([]logoCell3, len(m.blocks))
	for index, block := range m.blocks {
		positions[index] = block.home
	}
	steps := [][]logoCell3{positions}
	lastMoved := map[int]bool{}
	random := float64(cycle)*7_919 + 1
	next := func() float64 {
		value := logoHash(random)
		random++
		return value
	}
	key := func(cell logoCell3) int {
		return ((int(cell[2])+1)*rows+int(cell[1]))*columns + int(cell[0])
	}
	type candidate struct {
		block  int
		target logoCell3
	}
	var candidates, fresh []candidate
	for range logoPuzzleSteps {
		occupied := make(map[int]bool, len(positions))
		for _, position := range positions {
			occupied[key(position)] = true
		}
		nextPositions := append([]logoCell3(nil), positions...)
		moved := map[int]bool{}
		// Ordered as Pi's Set iterates: by insertion.
		var movedOrder []int
		moveCount := m.puzzleMoves(next())
		for range moveCount {
			candidates = candidates[:0]
			for block, position := range positions {
				if moved[block] {
					continue
				}
				for _, delta := range egg3dPuzzleMoves {
					target := logoCell3{position[0] + delta[0], position[1] + delta[1], position[2] + delta[2]}
					if target[0] < 0 || target[0] >= float64(columns) || target[1] < 0 || target[1] >= float64(rows) {
						continue
					}
					if target[2] < -1 || target[2] > 1 || occupied[key(target)] {
						continue
					}
					candidates = append(candidates, candidate{block, target})
				}
			}
			// Prefer blocks that did not just move, so the puzzle does not look like one block jittering.
			fresh = fresh[:0]
			for _, c := range candidates {
				if !lastMoved[c.block] {
					fresh = append(fresh, c)
				}
			}
			pool := candidates
			if len(fresh) > 0 {
				pool = fresh
			}
			choice := int(math.Floor(next() * float64(len(pool))))
			if choice >= len(pool) {
				break
			}
			occupied[key(pool[choice].target)] = true
			nextPositions[pool[choice].block] = pool[choice].target
			if !moved[pool[choice].block] {
				movedOrder = append(movedOrder, pool[choice].block)
			}
			moved[pool[choice].block] = true
		}
		clear(lastMoved)
		for _, block := range movedOrder {
			lastMoved[block] = true
		}
		positions = nextPositions
		steps = append(steps, positions)
	}
	return steps
}

// egg3dShuffle caches one cycle's shuffle steps.
type egg3dShuffle struct {
	cycle int
	steps [][]logoCell3
}

// blockOffsets is each block's displacement from its place in the model at time, in grid units: home until the puzzle
// starts, then each cycle shuffles, flies every block back home and holds.
func (a *pigLogoAnimation) blockOffsets(time float64) [][3]float64 {
	blocks := a.model.blocks
	offsets := make([][3]float64, len(blocks))
	if a.model.puzzleMoves == nil || time < logoPuzzleStart {
		return offsets
	}
	cycle := int(math.Floor((time - logoPuzzleStart) / logoPuzzleCycle))
	local := time - logoPuzzleStart - float64(cycle)*logoPuzzleCycle
	if a.shuffle == nil || a.shuffle.cycle != cycle {
		a.shuffle = &egg3dShuffle{cycle: cycle, steps: a.model.shuffleSteps(cycle)}
	}
	steps := a.shuffle.steps
	offset := func(position logoCell3, index int) [3]float64 {
		home := blocks[index].home
		return [3]float64{position[0] - home[0], position[1] - home[1], position[2] - home[2]}
	}
	if local < logoShuffleEnd {
		step := int(math.Floor(local / logoPuzzleStep))
		progress := logoSmooth((local - float64(step)*logoPuzzleStep) / logoPuzzleStep)
		for index, from := range steps[step] {
			a, b := offset(from, index), offset(steps[step+1][index], index)
			offsets[index] = [3]float64{a[0] + (b[0]-a[0])*progress, a[1] + (b[1]-a[1])*progress, a[2] + (b[2]-a[2])*progress}
		}
		return offsets
	}
	if local < logoShuffleEnd+logoPuzzleReturn {
		// All blocks fly home at once. Alternating arcs in depth keep them from passing through each other.
		u := (local - logoShuffleEnd) / logoPuzzleReturn
		remaining := 1 - logoSmooth(u)
		arc := math.Sin(math.Pi*clamp01(u)) * 0.8
		for index, position := range steps[logoPuzzleSteps] {
			o := offset(position, index)
			if index%2 != 0 {
				offsets[index] = [3]float64{o[0] * remaining, o[1] * remaining, o[2]*remaining - arc}
			} else {
				offsets[index] = [3]float64{o[0] * remaining, o[1] * remaining, o[2]*remaining + arc}
			}
		}
	}
	return offsets
}
