// SPDX-License-Identifier: MIT

package config

import (
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/config/internal/ini"
)

// updateInt stores what fits the platform's int exactly (every int64 on a 64-bit build, as upstream does) and rejects the rest: on a
// 32-bit build a value above 2^31-1 is an error instead of a wrapped setting.
func TestUpdateIntFollowsThePlatformIntSize(t *testing.T) {
	wide := strconv.IntSize == 64
	for _, tc := range []struct {
		value string
		want  int64
		ok    bool
	}{
		{"5", 5, true},
		{"-2147483648", -2147483648, true},
		{"2147483647", 2147483647, true},
		{"0x7fffffff", 2147483647, true},
		{"2147483648", 2147483648, wide},
		{"-2147483649", -2147483649, wide},
		{"9223372036854775807", 9223372036854775807, wide},
		{"9223372036854775808", 0, false},
		{"abc", 0, false},
	} {
		section := ini.NewSection("profile")
		v, err := ini.NewStringValue(tc.value)
		if err != nil {
			t.Fatal(err)
		}
		if err := section.UpdateValue("max_attempts", v); err != nil {
			t.Fatal(err)
		}
		var dst int
		err = updateInt(&dst, section, "max_attempts")
		if tc.ok != (err == nil) {
			t.Errorf("updateInt(%s): error %v, want ok=%v", tc.value, err, tc.ok)
			continue
		}
		if tc.ok && int64(dst) != tc.want {
			t.Errorf("updateInt(%s) = %d, want %d", tc.value, dst, tc.want)
		}
		if !tc.ok && dst != 0 {
			t.Errorf("updateInt(%s) wrote %d on error", tc.value, dst)
		}
	}
}

func TestSetIntFromEnvValRejectsAValueBeyondInt(t *testing.T) {
	t.Setenv("PIG_TEST_AWS_INT", "99999999999999999999")
	var dst int
	if err := setIntFromEnvVal(&dst, []string{"PIG_TEST_AWS_INT"}); err == nil || dst != 0 {
		t.Fatalf("out-of-range value: dst=%d err=%v", dst, err)
	}
	t.Setenv("PIG_TEST_AWS_INT", "42")
	if err := setIntFromEnvVal(&dst, []string{"PIG_TEST_AWS_INT"}); err != nil || dst != 42 {
		t.Fatalf("dst=%d err=%v", dst, err)
	}
}
