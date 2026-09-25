package mermaid

import (
	"strings"
	"testing"
)

// A diagram wider than the area used to be replaced by its source. Narrowing the
// node labels draws it instead, and the label width is chosen by measuring real
// layouts rather than from a fixed table, so the result lands as close under the
// target as the diagram allows.
const fitFixture = `flowchart LR
  subgraph NB["Notebook"]
    direction TB
    NBCFG["the config file has many short words in it"]
    NBENV["the env file also has short words in it"]
  end
  subgraph PG["Pig"]
    direction TB
    PGPIG["the piglet file lists the tools it may use"]
    PGGW["the model code is about one seven five lines"]
  end
  NBCFG -. "one two three" .-> PGPIG
  NBENV -. "four five six" .-> PGGW
  PGPIG ==> PGGW`

func TestRenderWithinNarrowsUntilTheDiagramFits(t *testing.T) {
	natural, ok := Render(fitFixture)
	if !ok {
		t.Fatal("the fixture must render at its natural width")
	}

	fitted := 0
	for _, target := range []int{natural.Width - 10, natural.Width - 30, natural.Width - 50} {
		art, ok := RenderWithin(fitFixture, target)
		if !ok {
			t.Errorf("target %d: refused to render", target)
			continue
		}
		if art.splitWord {
			t.Errorf("target %d: sliced a word to reach %d columns", target, art.Width)
		}
		if art.Width <= target {
			fitted++
			continue
		}
		// Not fitting is allowed: boxes, arrows and the diagram's own longest
		// word set a floor. It must still have narrowed as far as that floor.
		if art.Width >= natural.Width {
			t.Errorf("target %d: returned the natural %d columns without narrowing", target, natural.Width)
		}
	}
	if fitted == 0 {
		t.Error("no target was met, so this fixture proves nothing about narrowing")
	}
}

// A diagram that already fits must come back exactly as Render draws it, so the
// common case is untouched and stays byte-identical to upstream's renderer.
func TestRenderWithinLeavesAFittingDiagramAlone(t *testing.T) {
	natural, ok := Render(fitFixture)
	if !ok {
		t.Fatal("the fixture must render at its natural width")
	}
	art, ok := RenderWithin(fitFixture, natural.Width)
	if !ok {
		t.Fatal("RenderWithin refused a diagram that already fits")
	}
	if art.Width != natural.Width || len(art.Plain) != len(natural.Plain) {
		t.Fatalf("a fitting diagram was altered: %d×%d became %d×%d",
			natural.Width, len(natural.Plain), art.Width, len(art.Plain))
	}
	for i := range natural.Plain {
		if art.Plain[i] != natural.Plain[i] {
			t.Fatalf("row %d differs:\n%q\n%q", i, art.Plain[i], natural.Plain[i])
		}
	}
}

// Boxes, arrows and parallel branches take columns no label shrink can recover,
// so below that floor RenderWithin reports the narrowest art it managed and
// leaves the caller to decide. Silently claiming a fit would be worse.
func TestRenderWithinReportsTheStructuralFloor(t *testing.T) {
	art, ok := RenderWithin(fitFixture, 1)
	if !ok {
		t.Fatal("RenderWithin gave up instead of returning its narrowest attempt")
	}
	if art.Width <= 1 {
		t.Fatalf("width %d is below the structural floor, so the art is not real", art.Width)
	}
	narrowest, _ := RenderWithin(fitFixture, 10)
	if art.Width != narrowest.Width {
		t.Errorf("two impossible targets gave %d and %d; the floor is not deterministic",
			art.Width, narrowest.Width)
	}
}

// An unmeasured area cannot judge any diagram, so the natural layout is returned.
func TestRenderWithinIgnoresAnUnmeasuredArea(t *testing.T) {
	natural, _ := Render(fitFixture)
	art, ok := RenderWithin(fitFixture, 0)
	if !ok || art.Width != natural.Width {
		t.Errorf("width 0 gave %d, want the natural %d", art.Width, natural.Width)
	}
}

// Narrowing must cost rows, never words. The first version of RenderWithin let
// labels keep their four-line budget, so a narrower label simply lost its tail
// to an ellipsis: a diagram that looked drawn while saying less than the raw
// source it replaced. That is worse than not drawing it.
func TestNarrowingNeverTruncatesALabel(t *testing.T) {
	const src = `flowchart LR
  subgraph NB["Notebook"]
    direction TB
    NBCFG["notebooks/config.yaml model type, file paths, chunk size 70 to 50"]
    NBENV["notebooks/data/.env 8 environment variables per user"]
  end
  subgraph PG["Pig"]
    direction TB
    PGPIG["triplel.piglet.yaml tool whitelist here"]
  end
  NBCFG -. "composition, not wiring" .-> PGPIG
  NBENV -. "auth moves to the gateway" .-> PGPIG`

	natural, ok := Render(src)
	if !ok {
		t.Fatal("the fixture must render at its natural width")
	}
	if strings.Contains(strings.Join(natural.Plain, ""), "…") {
		t.Fatal("the fixture already truncates at its natural width, so it proves nothing")
	}

	for target := natural.Width; target >= 20; target -= 5 {
		art, ok := RenderWithin(src, target)
		if !ok {
			t.Fatalf("target %d: refused to render", target)
		}
		if strings.Contains(strings.Join(art.Plain, ""), "…") {
			t.Errorf("target %d: narrowing to %d columns truncated a label", target, art.Width)
		}
	}
}

// Rows are what narrowing spends instead of words.
func TestNarrowingSpendsRowsInsteadOfWords(t *testing.T) {
	natural, _ := Render(fitFixture)
	narrowed, ok := RenderWithin(fitFixture, natural.Width-30)
	if !ok {
		t.Fatal("RenderWithin refused the fixture")
	}
	if narrowed.Width >= natural.Width {
		t.Fatalf("no narrowing happened: %d then %d", natural.Width, narrowed.Width)
	}
	if len(narrowed.Plain) < len(natural.Plain) {
		t.Errorf("narrowing to %d columns lost rows (%d then %d), so content went missing",
			narrowed.Width, len(natural.Plain), len(narrowed.Plain))
	}
}

// The legibility contract, stated once: narrowing never introduces a word break
// the natural layout did not already have, at any target, for any diagram.
// Narrowing past a diagram's longest word produces a column of fragments harder
// to read than the source it replaces, so the floor is the diagram's own
// vocabulary rather than a constant someone picked.
//
// A word longer than the natural wrap width is sliced by grok-mermaid itself.
// That is upstream behavior and is preserved; the contract is that fitting adds
// none of its own.
func TestRenderWithinNeverReturnsSlicedWords(t *testing.T) {
	sources := []string{
		fitFixture,
		"flowchart LR\n  A[\"antidisestablishmentarianism\"] --> B[\"short\"]",
		"flowchart TB\n  A[\"one\"] --> B[\"two\"] --> C[\"three\"] --> D[\"four\"]",
		"flowchart LR\n  subgraph S[\"frame\"]\n    X[\"internationalization\"]\n  end\n  X --> Y[\"y\"]",
	}
	for _, src := range sources {
		natural, ok := Render(src)
		if !ok {
			t.Fatalf("fixture must render: %q", src)
		}
		for target := 1; target <= natural.Width; target++ {
			art, ok := RenderWithin(src, target)
			if !ok {
				t.Errorf("target %d refused: %q", target, src)
				break
			}
			if art.splitWord && !natural.splitWord {
				t.Errorf("target %d sliced a word the natural layout kept whole (%d columns): %q",
					target, art.Width, src)
				break
			}
		}
	}
}

// A diagram carrying a word long enough to bound how far it can narrow still
// narrows to that bound rather than giving up at its natural width.
//
// This does not pin the search's direction when it meets a sliced candidate.
// Getting that branch backwards costs width, not correctness: the best
// non-slicing candidate already seen is still what comes back, so the result is
// wider than necessary rather than wrong, and no fixture here separates the two.
func TestALongWordBoundsNarrowingWithoutStoppingIt(t *testing.T) {
	const src = `flowchart LR
  A["configuration model type and the file paths it reads"] --> B["short one"]
  B --> C["another short label here"]`

	natural, ok := Render(src)
	if !ok {
		t.Fatal("the fixture must render at its natural width")
	}
	art, ok := RenderWithin(src, natural.Width/2)
	if !ok {
		t.Fatal("RenderWithin refused the fixture")
	}
	if art.splitWord {
		t.Fatal("the returned layout sliced a word")
	}
	if art.Width >= natural.Width {
		t.Fatalf("no narrowing happened: natural %d, returned %d; the search moved the "+
			"wrong way when it met the long word", natural.Width, art.Width)
	}
}
