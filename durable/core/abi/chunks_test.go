// SPDX-License-Identifier: MIT

package abi

import (
	"bytes"
	"testing"
)

// Chunks grows its first block by append up to ChunkSize, then starts blocks of ChunkSize, or of one item when the item
// is larger; an item never straddles two blocks, and the payload is the blocks' concatenation.
func TestChunksBlocksConcatenateToTheItems(t *testing.T) {
	var c Chunks
	var want []byte
	for i := range 3000 {
		item := bytes.Repeat([]byte{byte('a' + i%26)}, 1+i%97)
		if i == 1500 {
			item = bytes.Repeat([]byte("z"), 3*ChunkSize)
		}
		c.Append(item)
		want = append(want, item...)
		if last := c[len(c)-1]; !bytes.HasSuffix(last, item) {
			t.Fatalf("item %d straddles two blocks", i)
		}
	}
	if len(c) < 3 {
		t.Fatalf("%d blocks for %d bytes", len(c), len(want))
	}
	for i, b := range c {
		if len(b) > ChunkSize && len(b) != 3*ChunkSize {
			t.Fatalf("block %d holds %d bytes, over ChunkSize without an item that large", i, len(b))
		}
	}
	if c.Len() != len(want) || !bytes.Equal(bytes.Join(c, nil), want) {
		t.Fatalf("the blocks concatenate to %d bytes, want %d", c.Len(), len(want))
	}
	n := Notice{Kind: NoticePublished, Chunks: c}
	if got := n.Data(); !bytes.Equal(got, want) {
		t.Fatal("Notice.Data is not the blocks' concatenation")
	}
	e := Effect{Kind: EffectHops, Payload: want}
	if got := e.Data(); &got[0] != &want[0] {
		t.Fatal("Effect.Data copies a payload in one slice")
	}
}

// AppendHopsChunks writes AppendHops's bytes, so a chunked hops payload decodes on the host unchanged.
func TestAppendHopsChunksIsAppendHops(t *testing.T) {
	text := Text(string(bytes.Repeat([]byte("t"), 200)))
	var commits []Commit
	for seq := range 2000 {
		commits = append(commits, Commit{Seq: int64(seq), Stmts: []Stmt{{SQL: 3, Params: []Value{Int(int64(seq)), text, Null()}}, {SQL: 4}, {SQL: 5, Params: []Value{Blob(make([]byte, seq%300))}}}})
	}
	for _, h := range []Hops{
		{N: 3},
		{N: 5, At: 2, Commits: commits[:1]},
		{N: 6, At: 1, Commits: commits},
	} {
		var c Chunks
		AppendHopsChunks(&c, h)
		want := AppendHops(nil, h)
		if got := bytes.Join(c, nil); !bytes.Equal(got, want) {
			t.Fatalf("%d commits: AppendHopsChunks wrote %d bytes, AppendHops %d (or they differ)", len(h.Commits), len(got), len(want))
		}
		if len(h.Commits) == len(commits) && len(c) < 2 {
			t.Fatalf("a %d-byte payload is one block", len(want))
		}
	}
}

// A chunked notice or effect has the wire form of its payload in one slice. AppendStepParts hands each block to the
// host as a part of its own, and StepSizeInline does not count the blocks.
func TestChunkedPayloadsKeepTheWireForm(t *testing.T) {
	var c Chunks
	for i := range 4 * ChunkSize / 100 {
		c.Append(bytes.Repeat([]byte{byte('a' + i%26)}, 100))
	}
	data := bytes.Join(c, nil)
	chunked := &Step{
		Notices: []Notice{{Kind: NoticeReport, Payload: []byte("r")}, {Kind: NoticePublished, Chunks: c}},
		Effects: []Effect{{ID: 1, Kind: EffectHops, Chunks: c}},
		Order:   []OrderRef{{Region: OrderNotice, Index: 0}, {Region: OrderEffect, Index: 0}, {Region: OrderNotice, Index: 1}},
	}
	flat := &Step{
		Notices: []Notice{{Kind: NoticeReport, Payload: []byte("r")}, {Kind: NoticePublished, Payload: data}},
		Effects: []Effect{{ID: 1, Kind: EffectHops, Payload: data}},
		Order:   chunked.Order,
	}
	want := AppendStep(nil, flat)
	if got := AppendStep(nil, chunked); !bytes.Equal(got, want) {
		t.Fatal("AppendStep of chunked payloads differs from the same payloads in one slice")
	}
	if StepSize(chunked) != len(want) {
		t.Fatalf("StepSize %d, wire length %d", StepSize(chunked), len(want))
	}
	buf, parts := AppendStepParts(nil, chunked, 1<<20, nil)
	if len(buf) != StepSizeInline(chunked, 1<<20) {
		t.Fatalf("the buffer holds %d bytes, StepSizeInline %d", len(buf), StepSizeInline(chunked, 1<<20))
	}
	var got []byte
	blocks := 0
	for _, p := range parts {
		if p.Ext != nil {
			if &p.Ext[0] != &c[blocks%len(c)][0] {
				t.Fatalf("part %d is not block %d", blocks, blocks%len(c))
			}
			blocks++
			got = append(got, p.Ext...)
			continue
		}
		got = append(got, buf[p.Off:p.End]...)
	}
	if blocks != 2*len(c) || !bytes.Equal(got, want) {
		t.Fatalf("%d block parts (want %d); the parts concatenate to the wire form: %v", blocks, 2*len(c), bytes.Equal(got, want))
	}
	decoded, err := DecodeStep(want)
	if err != nil || !bytes.Equal(decoded.Notices[1].Payload, data) || decoded.Notices[1].Chunks != nil || !bytes.Equal(decoded.Effects[0].Payload, data) {
		t.Fatalf("decoded %v", err)
	}
}
