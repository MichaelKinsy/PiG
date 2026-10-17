// SPDX-FileCopyrightText: Copyright Hewlett Packard Enterprise Development LP
// SPDX-License-Identifier: MIT

package session

import (
	"errors"
	"testing"
)

func TestSprintfFormatsTheVerbsPisMessagesUse(t *testing.T) {
	cause := errors.New("boom")
	for _, c := range []struct {
		format string
		args   []any
		want   string
	}{
		{"Task %d is %s", []any{7, "terminal"}, "Task 7 is terminal"},
		{"Task %d is %s", []any{ID(1 << 40), "x"}, "Task 1099511627776 is x"},
		{"%s %d: %v", []any{"Fork", int64(-3), cause}, "Fork -3: boom"},
		{"number %v", []any{1.5}, "number 1.5"},
		{"100%% of %d", []any{2}, "100% of 2"},
		{"plain", nil, "plain"},
		{"trailing %", nil, "trailing %"},
	} {
		if got := sprintf(c.format, c.args...); got != c.want {
			t.Errorf("sprintf(%q) = %q, want %q", c.format, got, c.want)
		}
	}
	if err := errorf("Task %d does not exist", 4); err.Error() != "Task 4 does not exist" {
		t.Errorf("errorf = %v", err)
	}
}

func TestSprintfRejectsWhatItCannotFormat(t *testing.T) {
	for name, call := range map[string]func(){
		"verb":     func() { sprintf("%x", 1) },
		"argument": func() { sprintf("%d", struct{}{}) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			call()
		}()
	}
}
