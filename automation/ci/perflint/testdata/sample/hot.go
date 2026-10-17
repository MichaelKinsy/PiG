package sample

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Hop hands off per call in every way chan-hop counts.
//
//perf:hot
func Hop(ctx context.Context, ch chan int) {
	ch <- 1
	<-ch
	_ = make(chan int)
	go func() { ch <- 2 }()
	context.AfterFunc(ctx, func() {})
	time.AfterFunc(time.Second, func() {})
	for range ch {
	}
}

//pig:hotpath
func Open(name string) {
	f, _ := os.Open(name)
	_ = f.Close()
	_, _ = os.ReadFile(name)
}

//perf:hot once per frame
func Timer() {
	t := time.NewTimer(time.Second)
	t.Stop()
	_ = time.After(time.Second)
}

// Cold is not hot: nothing here counts.
func Cold(ch chan int, name string) {
	ch <- 1
	_, _ = os.ReadFile(name)
	_ = time.NewTimer(time.Second)
}

//pig:hotpathless
func NotHot(ch chan int) { ch <- 1 }

//perf:hot
func HotAllowed(ch chan int) {
	//perf:ok one acknowledgement per run, not per event
	<-ch
}

// Retry makes a timer per iteration of a loop in a hot package.
func Retry(n int) {
	for range n {
		<-time.After(time.Millisecond)
	}
}

type Entry struct{ A int }

// MarshalJSON marshals its receiver through a plain copy: not nested.
func (e Entry) MarshalJSON() ([]byte, error) {
	type plain Entry
	return json.Marshal(plain(e))
}

func DecodeEntry(b []byte) (Entry, error) {
	var e Entry
	err := json.Unmarshal(b, &e)
	return e, err
}

func Own(e Entry) (Entry, error) {
	b, _ := e.MarshalJSON()
	return DecodeEntry(b)
}

func OwnStream(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.NewDecoder(bytes.NewReader(b)).Decode(&out)
	return out
}

type Wrapper struct {
	Inner Entry
	Name  string
}

func (w Wrapper) MarshalJSON() ([]byte, error) {
	inner, err := json.Marshal(w.Inner)
	if err != nil {
		return nil, err
	}
	again, _ := w.Inner.MarshalJSON()
	return append(append(inner, again...), w.Name...), nil
}

var bufPool = sync.Pool{New: func() any { return new(bytes.Buffer) }}

func PutUncapped(b *bytes.Buffer) {
	b.Reset()
	bufPool.Put(b)
}

func PutCapped(b *bytes.Buffer) {
	if b.Cap() > 64<<10 {
		return
	}
	b.Reset()
	bufPool.Put(b)
}

type small struct{ n int }

var smallPool sync.Pool

func PutSmall(s *small) { smallPool.Put(s) }
