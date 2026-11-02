// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

// Command scprovider serves the core's write encoder to the storage conformance suite (CONTRACT 3.10, the SC row): the
// suite's Storage is pi-durable's SqliteStorage for every read, and this process for commit and mintId. It speaks one JSON
// object per line on stdin and stdout:
//
//	{"id":1,"op":"open","path":"/x/store.sqlite"}     the file pi-durable already created (schema, metadata); cold-opens the store
//	{"id":2,"op":"mint"}                              -> {"id":2,"ok":<id>}
//	{"id":3,"op":"commit","batches":[[StorageWrite...]...]} -> {"id":3,"ok":{"seqs":[<seq>...],"refused":{"name","message"}?}}
//	{"id":4,"op":"close"}                             checkpoints the WAL and exits
//
// A failure answers {"id":n,"error":{"name":"Error","message":...}}, with the message SqliteStorage would throw.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/MichaelKinsy/PiG/durable/core/contracttest/storehost"
	"github.com/MichaelKinsy/PiG/durable/core/history"
	"github.com/MichaelKinsy/PiG/durable/core/jv"
	"github.com/MichaelKinsy/PiG/durable/core/rec"
	"github.com/MichaelKinsy/PiG/durable/core/store"
)

type server struct {
	h  *storehost.Host
	st *store.Store
}

func main() {
	in := bufio.NewReaderSize(os.Stdin, 1<<20)
	out := bufio.NewWriter(os.Stdout)
	var s server
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			reply, done := s.handle(line)
			_, _ = out.WriteString(reply + "\n")
			_ = out.Flush()
			if done {
				return
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) {
				fmt.Fprintln(os.Stderr, "scprovider:", err)
				os.Exit(1)
			}
			return
		}
	}
}

func get(o *jv.Object, k string) any { v, _ := o.Get(k); return v }

func id(v any) int64 { f, _ := v.(float64); return int64(f) }

func (s *server) handle(line []byte) (reply string, done bool) {
	v, err := jv.Parse(line)
	req, _ := v.(*jv.Object)
	if err != nil || req == nil {
		return fail(nil, errors.New("bad request")), false
	}
	reqID := get(req, "id")
	var result any
	switch get(req, "op") {
	case "open":
		result, err = s.open(get(req, "path").(string))
	case "mint":
		// SqliteStorage.mintId rejects once the IDs run past Number.MAX_SAFE_INTEGER (storage-conformance.ts "exhausted ID minting").
		if s.st.NextID() > 1<<53-1 {
			err = errors.New("ID space is exhausted")
			break
		}
		result = float64(s.st.MintID())
	case "commit":
		result, err = s.commit(get(req, "batches").([]any))
	case "close":
		if s.h != nil {
			err = s.h.Close()
		}
		done = true
	default:
		err = fmt.Errorf("unknown op %v", get(req, "op"))
	}
	if err != nil {
		return fail(reqID, err), done
	}
	o := jv.NewObjectCap(2)
	o.Set("id", reqID)
	o.Set("ok", result)
	return jv.Stringify(o), done
}

func fail(reqID any, err error) string {
	name := "Error"
	e := jv.NewObjectCap(2)
	e.Set("name", name)
	e.Set("message", err.Error())
	o := jv.NewObjectCap(2)
	o.Set("id", reqID)
	o.Set("error", e)
	return jv.Stringify(o)
}

func (s *server) open(path string) (any, error) {
	h, err := storehost.Open(path, nil)
	if err != nil {
		return nil, err
	}
	st := store.New(store.Options{})
	if err := h.Run(st, func() error { _, err := st.Open(h.B); return err }); err != nil {
		return nil, err
	}
	s.h, s.st = h, st
	return nil, nil
}

// commit is Storage.commit(batches): each batch one commit with its own seq, checked against the state the earlier ones left, all of them one
// transaction for the host (the commits are joined, ABI section 4.4). It stops at the first batch the ID checks refuse; the batches before it are
// durable, and the answer carries its refusal.
func (s *server) commit(batches []any) (any, error) {
	var refused error
	claimed := map[int64]string{}
	stored := batches
	for i, b := range batches {
		if err := s.checkBatch(b.([]any), claimed); err != nil {
			refused, stored = err, batches[:i]
			break
		}
	}
	var seqs []any
	err := s.h.Run(s.st, func() error {
		seqs = seqs[:0]
		for i, b := range stored {
			tx := s.st.Begin()
			for _, w := range b.([]any) {
				if err := stage(tx, w.(*jv.Object)); err != nil {
					return err
				}
			}
			seq, err := s.st.Commit(s.h.B, tx)
			if err != nil {
				return err
			}
			if i > 0 {
				s.h.B.JoinLast()
			}
			seqs = append(seqs, float64(seq))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := jv.NewObjectCap(2)
	out.Set("seqs", append([]any(nil), seqs...))
	if refused != nil {
		e := jv.NewObjectCap(2)
		e.Set("name", "Error")
		e.Set("message", refused.Error())
		out.Set("refused", e)
	}
	return out, nil
}

// checkBatch is the checks of Storage.commit that need nothing but the batch and the committed record IDs: the global IDs
// (checkIDs) and the document actions of the batch (checkDocuments).
func (s *server) checkBatch(writes []any, claimed map[int64]string) error {
	if err := s.checkIDs(writes, claimed); err != nil {
		return err
	}
	return checkDocuments(writes)
}

// checkDocuments is prepareDocumentActions and the copy rule of checkDocumentActions (storage.ts): a document has one
// content command and one retire in a batch, and a copy's source is not touched by the batch that copies it.
func checkDocuments(writes []any) error {
	type action struct {
		content, retire bool
		source          int64 // the copied document, when the batch copies it
	}
	actions := map[int64]*action{}
	var order []int64
	for _, item := range writes {
		w := item.(*jv.Object)
		kind := get(w, "type").(string)
		var rid int64
		switch kind {
		case "document.create", "document.copy":
			rid = id(get(get(w, "record").(*jv.Object), "id"))
		case "document.change", "document.retire":
			rid = id(get(w, "id"))
		default:
			continue
		}
		a := actions[rid]
		if a == nil {
			a = &action{}
			actions[rid] = a
			order = append(order, rid)
		}
		switch kind {
		case "document.create", "document.copy":
			if a.content {
				return errors.New("Document " + strconv.FormatInt(rid, 10) + " has more than one content command")
			}
			a.content = true
			if kind == "document.copy" {
				a.source = id(get(get(w, "source").(*jv.Object), "id"))
			}
		case "document.change":
			if a.content {
				return errors.New("Document " + strconv.FormatInt(rid, 10) + " has more than one content command")
			}
			a.content = true
		case "document.retire":
			if a.retire {
				return errors.New("Document " + strconv.FormatInt(rid, 10) + " is retired more than once")
			}
			a.retire = true
		}
	}
	for _, rid := range order {
		if source := actions[rid].source; source != 0 && actions[source] != nil {
			return errors.New("Document copy " + strconv.FormatInt(rid, 10) + " source is changed in the copy batch")
		}
	}
	return nil
}

// checkIDs is checkGlobalIds (storage.ts) against the committed record_ids: one namespace for every record. The core
// mints its own IDs, so its write encoder never reads that table; the suite writes explicit IDs. A conversation, entry
// or document ID must be new; a task or submission ID may repeat its own type.
func (s *server) checkIDs(writes []any, claimed map[int64]string) error {
	for _, item := range writes {
		w := item.(*jv.Object)
		kind := get(w, "type").(string)
		var rid any
		switch kind {
		case "conversation", "entry", "task", "submission":
			rid = get(get(w, "value").(*jv.Object), "id")
		case "document.create", "document.copy":
			rid = get(get(w, "record").(*jv.Object), "id")
		default:
			continue
		}
		known, err := s.h.RecordType(id(rid))
		if err != nil {
			return err
		}
		if earlier := claimed[id(rid)]; known == "" {
			known = earlier
		}
		own, _, _ := strings.Cut(kind, ".")
		if known != "" && (own != "task" && own != "submission" || known != own) {
			return errors.New("ID " + strconv.FormatInt(id(rid), 10) + " already belongs to " + known)
		}
		claimed[id(rid)] = own
	}
	return nil
}

func stage(tx *store.Tx, w *jv.Object) error {
	switch get(w, "type") {
	case "conversation":
		tx.Conversation(&rec.Conversation{V: get(w, "value").(*jv.Object)})
	case "entry":
		row, err := history.NewEntryRow(jv.Append(nil, get(w, "value").(*jv.Object)), 0)
		if err != nil {
			return err
		}
		tx.EntryRow(row)
	case "task":
		t := &rec.Task{V: get(w, "value").(*jv.Object)}
		tx.Task(t, t.Append(nil))
	case "submission":
		sub := &rec.Submission{V: get(w, "value").(*jv.Object)}
		tx.Submission(sub, sub.Append(nil))
	case "document.create":
		content := get(w, "content").(*jv.Object)
		tx.DocumentCreate(&rec.Document{V: get(w, "record").(*jv.Object)}, int(id(get(content, "version"))), get(content, "value"))
	case "document.copy":
		src := get(w, "source").(*jv.Object)
		at := int64(0)
		if f, ok := get(src, "at").(float64); ok {
			at = int64(f)
		}
		tx.DocumentCopy(&rec.Document{V: get(w, "record").(*jv.Object)}, id(get(src, "id")), at)
	case "document.change":
		content := get(w, "content").(*jv.Object)
		if get(content, "kind") == "base" {
			tx.DocumentBase(id(get(w, "id")), int(id(get(content, "version"))), get(content, "value"))
		} else {
			tx.DocumentDelta(id(get(w, "id")), int(id(get(content, "version"))), jv.Append(nil, get(content, "ops")))
		}
	case "document.retire":
		tx.DocumentRetire(id(get(w, "id")))
	default:
		return errors.New("unknown write type " + strconv.Quote(fmt.Sprint(get(w, "type"))))
	}
	return nil
}
