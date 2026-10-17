// SPDX-License-Identifier: MIT

// Command buildcheck links the history package into a program so that TinyGo (-target=wasip1 -scheduler=none),
// Go wasip1 and native Go build the same source. It derives a small context and exits non-zero on a wrong result.
package main

import (
	"os"

	"github.com/MichaelKinsy/PiG/durable/core/history"
)

func main() {
	st := history.NewStore()
	st.AddConversation(history.ConvRecord{ID: 2}, true)
	rec, err := history.AppendEntryRecord(nil, []byte(`{"kind":"pi.user","model":[{"role":"user","content":"hi","timestamp":1}]}`), 3, 2, 0, false)
	if err != nil {
		os.Exit(2)
	}
	if err := st.Append(2, 3, 1, rec); err != nil {
		os.Exit(3)
	}
	v, need, err := st.Context(2, 0)
	if err != nil || need != nil || v.NumMessages() != 1 {
		os.Exit(4)
	}
	if _, ok := v.SelectCut(1); ok {
		os.Exit(5)
	}
}
