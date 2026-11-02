package delta

import (
	"math/rand/v2"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/MichaelKinsy/PiG/internal/jsstring"
)

// textTails.concat is jsstring.Concat that shares bytes: every string it returned keeps its content however the buffers behind it grow, and its result is Concat's, byte for byte.

const (
	highSurrogate = "\xed\xa0\x80" // U+D800 in WTF-8
	lowSurrogate  = "\xed\xb0\x80" // U+DC00
)

func TestTextTailsConcatIsJSStringConcat(t *testing.T) {
	pieces := []string{"", "a", "héllo", "😀", highSurrogate, lowSurrogate, "x" + highSurrogate, lowSurrogate + "y", strings.Repeat("z", 40)}
	random := rand.New(rand.NewPCG(1, 2))
	var tails textTails
	// Every string a concat returned, with the string it must stay.
	type made struct{ got, want string }
	var history []made
	current := ""
	for range 3_000 {
		if len(history) > 0 && random.IntN(5) == 0 {
			// Branch from an earlier string, as a competing change does.
			current = history[random.IntN(len(history))].got
		}
		piece := pieces[random.IntN(len(pieces))]
		got := tails.concat(current, piece)
		if want := jsstring.Concat(current, piece); got != want {
			t.Fatalf("concat(%q, %q) = %q, want %q", current, piece, got, want)
		}
		history = append(history, made{got, jsstring.Concat(current, piece)})
		current = got
	}
	for index, entry := range history {
		if entry.got != entry.want {
			t.Fatalf("string %d changed to %q, was %q", index, entry.got, entry.want)
		}
	}
}

func TestTextTailsSharesTheBytesOfAGrowingText(t *testing.T) {
	var tails textTails
	text := tails.concat("start", "-1")
	for range 100 {
		text = tails.concat(text, "-n")
	}
	if allocated := testing.AllocsPerRun(50, func() { text = tails.concat(text, "-n") }); allocated != 0 {
		t.Fatalf("an append to a growing text allocated %v times per call, want none on average", allocated)
	}
	if want := "start-1" + strings.Repeat("-n", 100+51); text != want {
		t.Fatalf("text %q, want %q", text, want)
	}
}

func TestTextTailsRepeatedAppendReturnsTheStringAlreadyMade(t *testing.T) {
	var tails textTails
	base := tails.concat("base", "-0")
	base = tails.concat(base, "-1")
	grown := tails.concat(base, "-2")
	again := tails.concat(base, "-2")
	if again != grown || unsafe.StringData(again) != unsafe.StringData(grown) { //nolint:gosec // G103: the test compares the data address of strings that must share
		t.Fatalf("appending the same text to the same string made a second copy")
	}
}

func TestTextTailsBranchesFromOneStringStayApart(t *testing.T) {
	var tails textTails
	base := tails.concat("base", "-0")
	base = tails.concat(base, "-1")
	left := tails.concat(base, "-left")
	right := tails.concat(base, "-right")
	left2 := tails.concat(left, "-more")
	if left != "base-0-1-left" || right != "base-0-1-right" || left2 != "base-0-1-left-more" || base != "base-0-1" {
		t.Fatalf("branches %q %q %q from %q", left, right, left2, base)
	}
}

func TestTextTailsKeepsGrowingPastTheBound(t *testing.T) {
	var tails textTails
	texts := make([]string, 3*maxTextTails)
	for round := range 5 {
		for index := range texts {
			texts[index] = tails.concat(texts[index], strings.Repeat(string(rune('a'+index%26)), round+1))
		}
	}
	for index, text := range texts {
		want := ""
		for round := range 5 {
			want += strings.Repeat(string(rune('a'+index%26)), round+1)
		}
		if text != want {
			t.Fatalf("text %d is %q, want %q", index, text, want)
		}
	}
	if len(tails.tails) > maxTextTails {
		t.Fatalf("%d buffers kept, want at most %d", len(tails.tails), maxTextTails)
	}
}

func TestSyncTextTailsConcurrentAppends(t *testing.T) {
	var shared syncTextTails
	base := shared.concat("base", "-0")
	base = shared.concat(base, "-1")
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			suffix := strings.Repeat(string(rune('a'+worker)), 3)
			text := base
			for range 200 {
				text = shared.concat(text, suffix)
			}
			if want := "base-0-1" + strings.Repeat(suffix, 200); text != want {
				t.Errorf("worker %d built %d bytes, want %d", worker, len(text), len(want))
			}
		})
	}
	wg.Wait()
}

// Every revision, and every op a commit emitted, keeps its text while later appends grow the same buffer, also past the surrogate halves a split append leaves open. The expected texts are built apart from the tracker, and cloned so they share no bytes with it.
func TestTrackerAppendTextStreamKeepsEarlierRevisions(t *testing.T) {
	deltas := []string{"ab", "héllo", highSurrogate, lowSurrogate + "x", "😀", strings.Repeat("long", 30), "z"}
	tracker := Track(JsonObjectOf("text", ""))
	type commit struct {
		revision, op string // the tracker's strings
		want, wantOp string // the independent expectation
	}
	var commits []commit
	expected := ""
	for index := range 400 {
		piece := deltas[index%len(deltas)]
		change := tracker.BeginChange()
		if err := AppendText(change.State(), "text", piece); err != nil {
			t.Fatal(err)
		}
		prepared, err := change.Prepare()
		if err != nil {
			t.Fatal(err)
		}
		expected = jsstring.Concat(expected, piece)
		revision := prepared.Value().Value("text").(string)
		if revision != expected {
			t.Fatalf("commit %d: text %q, want %q", index, revision, expected)
		}
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
		op := prepared.Ops()[0][2].(string)
		if op != piece {
			t.Fatalf("commit %d: op text %q, want %q", index, op, piece)
		}
		commits = append(commits, commit{revision, op, strings.Clone(expected), strings.Clone(piece)})
	}
	for index, entry := range commits {
		if entry.revision != entry.want {
			t.Fatalf("revision %d changed to %q, was %q", index, entry.revision, entry.want)
		}
		if entry.op != entry.wantOp {
			t.Fatalf("op %d changed to %q, was %q", index, entry.op, entry.wantOp)
		}
	}
}

// Two changes from one revision append different texts to the string the revision shares with the buffer both would extend: each sees its own text.
func TestTrackerAppendTextCompetingChangesStayApart(t *testing.T) {
	tracker := Track(JsonObjectOf("text", ""))
	for range 5 {
		change := tracker.BeginChange()
		_ = AppendText(change.State(), "text", "base-")
		prepared, _ := change.Prepare()
		if err := tracker.Adopt(prepared); err != nil {
			t.Fatal(err)
		}
	}
	base := tracker.Value()
	first, second := tracker.BeginChange(), tracker.BeginChange()
	_ = AppendText(first.State(), "text", "left")
	_ = AppendText(second.State(), "text", "right")
	preparedSecond, err := second.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	preparedFirst, err := first.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := preparedFirst.Value().Value("text"), "base-base-base-base-base-left"; got != want {
		t.Fatalf("first change text %q, want %q", got, want)
	}
	if got, want := preparedSecond.Value().Value("text"), "base-base-base-base-base-right"; got != want {
		t.Fatalf("second change text %q, want %q", got, want)
	}
	if got, want := base.Value("text"), "base-base-base-base-base-"; got != want {
		t.Fatalf("base revision text %q, want %q", got, want)
	}
}

// Folded appends equal their jsstring.Concat, a taken fold stays what it was, and the queue starts again after Take.
func TestPendingOpsFoldedAppendsAreConcatenatedAndStayTaken(t *testing.T) {
	deltas := []string{"ab", highSurrogate, lowSurrogate + "x", "😀", "héllo", strings.Repeat("long", 30)}
	var pending PendingOps
	text := ""
	for index := range 500 {
		piece := deltas[index%len(deltas)]
		pending.Push([]Op{{"a", []any{"text"}, piece}})
		text = jsstring.Concat(text, piece)
	}
	taken := pending.Take()
	if len(taken.Ops) != 1 || taken.Ops[0][2] != text {
		t.Fatalf("taken %v, want one append of %q", taken.Ops, text)
	}
	for index := range 100 {
		pending.Push([]Op{{"a", []any{"text"}, deltas[index%len(deltas)]}})
	}
	if again := pending.Take(); len(again.Ops) != 1 || taken.Ops[0][2] != text {
		t.Fatalf("second fold %v, first now %q", again.Ops, taken.Ops[0][2])
	}
}

// Two appends to one key in one change join as JavaScript string concatenation: a lone high surrogate that ends the first and a lone low surrogate that starts the second make one character in the op, as they do in the new value.
func TestAppendTextJoinsSurrogateHalvesOfOneChange(t *testing.T) {
	tracker := Track(JsonObjectOf("text", "a"))
	change := tracker.BeginChange()
	for _, text := range []string{"b" + highSurrogate, lowSurrogate + "c"} {
		if err := AppendText(change.State(), "text", text); err != nil {
			t.Fatal(err)
		}
	}
	prepared, err := change.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	const joined = "b\U00010000c"
	if got := prepared.Ops(); len(got) != 1 || got[0][2] != joined {
		t.Fatalf("ops %v, want one append of %q", got, joined)
	}
	if got, want := prepared.Value().Value("text"), "a"+joined; got != want {
		t.Fatalf("value %q, want %q", got, want)
	}
}

// Buffers a textTails remembers are held weakly: texts made, dropped and collected, with their addresses reused by later buffers, never make a string find another text's buffer.
func TestTextTailsForgetCollectedBuffers(t *testing.T) {
	var tails textTails
	var keep []string
	for round := range 300 {
		word := strings.Repeat(string(rune('a'+round%26)), 1+round%7)
		text := ""
		for range 6 {
			text = tails.concat(text, word)
		}
		if want := strings.Repeat(word, 6); text != want {
			t.Fatalf("round %d: %q, want %q", round, text, want)
		}
		if round%3 == 0 {
			keep = append(keep, text)
		}
		if round%10 == 0 {
			runtime.GC()
		}
	}
	for index, text := range keep {
		round := index * 3
		if want := strings.Repeat(strings.Repeat(string(rune('a'+round%26)), 1+round%7), 6); text != want {
			t.Fatalf("kept text %d changed to %q, was %q", index, text, want)
		}
	}
}
