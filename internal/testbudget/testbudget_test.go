package testbudget

import (
	"testing"
	"time"
)

func TestResolve(t *testing.T) {
	for _, tc := range []struct {
		name          string
		override      string
		untilDeadline time.Duration
		hasDeadline   bool
		want          time.Duration
		wantErr       bool
	}{
		{name: "default without deadline", want: Default},
		{name: "override", override: "300", want: 300 * time.Second},
		{name: "test deadline caps the budget", untilDeadline: 40 * time.Second, hasDeadline: true, want: 30 * time.Second},
		{name: "distant deadline keeps default", untilDeadline: time.Hour, hasDeadline: true, want: Default},
		{name: "expired deadline keeps a positive budget", untilDeadline: time.Second, hasDeadline: true, want: time.Second},
		{name: "zero override", override: "0", wantErr: true},
		{name: "non-numeric override", override: "10s", wantErr: true},
		{name: "overflow override", override: "9223372037", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.override, tc.untilDeadline, tc.hasDeadline)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %t", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.want {
				t.Fatalf("budget = %s, want %s", got, tc.want)
			}
		})
	}
}
