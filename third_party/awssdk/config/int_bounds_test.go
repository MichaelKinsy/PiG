// SPDX-License-Identifier: MIT

package config

import (
	"strconv"
	"testing"

	"github.com/MichaelKinsy/PiG/third_party/awssdk/config/internal/ini"
)

func TestUpdateIntRejectsAValueBeyondTheIntegerRange(t *testing.T) {
	for value, ok := range map[string]bool{"5": true, "-2147483648": true, "2147483647": true, "2147483648": false, "9223372036854775807": false, "-2147483649": false} {
		section := ini.NewSection("profile")
		v, err := ini.NewStringValue(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := section.UpdateValue("max_attempts", v); err != nil {
			t.Fatal(err)
		}
		var dst int
		err = updateInt(&dst, section, "max_attempts")
		if ok != (err == nil) {
			t.Errorf("updateInt(%s): error %v, want ok=%v", value, err, ok)
		}
		if ok {
			if want, _ := strconv.Atoi(value); dst != want {
				t.Errorf("updateInt(%s) = %d", value, dst)
			}
		} else if dst != 0 {
			t.Errorf("updateInt(%s) wrote %d on error", value, dst)
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
