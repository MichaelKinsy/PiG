package jv

import (
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/MichaelKinsy/PiG/internal/testenv"
)

// TestKeysOfManyIndexKeysAreSortedInLogLinearTime checks that an object whose keys are many array indexes, inserted in any
// order, enumerates them ascending without a quadratic sort (task graph watches key an object by task id).
func TestKeysOfManyIndexKeysAreSortedInLogLinearTime(t *testing.T) {
	const n = 60000
	ids := rand.New(rand.NewSource(1)).Perm(n)
	o := NewObject()
	o.Set("name", "x")
	for _, id := range ids {
		o.Set(strconv.Itoa(id), float64(id))
	}
	start := time.Now()
	keys := o.Keys()
	elapsed := time.Since(start)
	if len(keys) != n+1 || keys[n] != "name" {
		t.Fatalf("keys: %d, last %q", len(keys), keys[len(keys)-1])
	}
	for i := range n {
		if keys[i] != strconv.Itoa(i) {
			t.Fatalf("key %d is %q", i, keys[i])
		}
	}
	// An insertion sort takes about 5 s here; a log-linear sort a few tens of milliseconds.
	testenv.TimingGate(t, elapsed > 2*time.Second, "Keys of %d shuffled index keys took %v: the sort is not log-linear", n, elapsed)
}
