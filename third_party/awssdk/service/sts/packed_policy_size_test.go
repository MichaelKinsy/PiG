// SPDX-License-Identifier: MIT

package sts

import (
	"encoding/xml"
	"strings"
	"testing"

	smithyxml "github.com/aws/smithy-go/encoding/xml"
)

// PackedPolicySize is an int32: a response whose value does not fit is a parse error, not a wrapped (here negative) size.
func TestAssumeRolePackedPolicySizeBeyondInt32IsAnError(t *testing.T) {
	decode := func(value string) (*AssumeRoleOutput, error) {
		decoder := xml.NewDecoder(strings.NewReader("<AssumeRoleResult><PackedPolicySize>" + value + "</PackedPolicySize></AssumeRoleResult>"))
		root, err := smithyxml.FetchRootElement(decoder)
		if err != nil {
			t.Fatal(err)
		}
		var out *AssumeRoleOutput
		err = awsAwsquery_deserializeOpDocumentAssumeRoleOutput(&out, smithyxml.WrapNodeDecoder(decoder, root))
		return out, err
	}
	out, err := decode("2147483647")
	if err != nil || out.PackedPolicySize == nil || *out.PackedPolicySize != 2147483647 {
		t.Fatalf("largest int32: %+v, %v", out, err)
	}
	for _, value := range []string{"2147483648", "4294967296", "-2147483649"} {
		if out, err := decode(value); err == nil {
			t.Errorf("PackedPolicySize %s decoded to %d without an error", value, *out.PackedPolicySize)
		}
	}
}
