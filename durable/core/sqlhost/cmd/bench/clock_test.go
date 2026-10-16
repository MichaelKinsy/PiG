// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package main

import (
	"sync"
	"testing"
)

// Values computed by node (JavaScript's Math.random().toString(36).slice(2) under detenv's generator, seed 1) and by
// (x).toString(36) for fixed doubles.
func TestFauxAPIMatchesTheReferenceRun(t *testing.T) {
	rng := &detenvRNG{seed: 1}
	if got, want := fauxAPI(1_800_000_000_000, rng.next()), "faux:1800000000000:mkorf98kqja"; got != want {
		t.Fatalf("fauxAPI = %q, want %q", got, want)
	}
}

// The faux provider's responses and the session draw from one generator on different goroutines. Each draw is one step of the
// stream, as each Math.random() is in detenv: concurrent draws leave the generator where as many sequential draws would, with
// no step lost (and no race under -race).
func TestDetenvRNGConcurrentDrawsLoseNoStep(t *testing.T) {
	const each = 1000
	shared := &detenvRNG{seed: 1}
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() {
			for range each {
				shared.next()
			}
		})
	}
	wg.Go(func() { shared.fill(make([]byte, each)) })
	wg.Wait()
	sequential := &detenvRNG{seed: 1}
	for range 3 * each {
		sequential.next()
	}
	if shared.seed != sequential.seed {
		t.Fatalf("after %d concurrent draws the generator is at %d, want %d", 3*each, shared.seed, sequential.seed)
	}
}

func TestBase36FractionMatchesJavaScript(t *testing.T) {
	for _, c := range []struct {
		v    float64
		want string
	}{
		{0.6270739405881613, "mkorf98kqja"},
		{0.002735721180215478, "03jmyljhjjv6"},
		{0.5274470399599522, "izkkhjrdd1"},
		{0.9810509674716741, "zbfwwgphmtc"},
		{0.5, "i"},
		{0.25, "9"},
		// V8 rounds fraction*36 before subtracting the digit; a fused multiply-add (arm64) changes the last digits.
		{0.6046602879796196, "lrn13e5ki3s"},
		{0.9405090880450124, "xuwe41pc6wj"},
		{0.6645600532184904, "nx9pp52264f"},
		{0.4246374970712657, "fabvxmtt2da"},
		{0.6868230728671094, "oq4f0sz2rye"},
	} {
		if got := base36Fraction(c.v); got != c.want {
			t.Errorf("base36Fraction(%v) = %q, want %q", c.v, got, c.want)
		}
	}
}

// pi-ai's uuidv7() under detenv after one Math.random() draw (node, seed 1, epoch 1800000000000): the two ids it returns.
func TestUUIDv7MatchesPiAIUnderDetenv(t *testing.T) {
	rng := &detenvRNG{seed: 1}
	rng.next()
	g := &uuidv7Gen{rng: rng, nowMillis: func() int64 { return 1_800_000_000_000 }}
	for _, want := range []string{"01a3185c-5000-743f-b7ee-8f39673f277d", "01a3185c-5000-743f-b7ee-8f3a8bc231b8"} {
		if got := g.next(); got != want {
			t.Fatalf("uuidv7 = %s, want %s", got, want)
		}
	}
}
