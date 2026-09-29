package ai

import (
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	btypes "github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
)

func TestBedrockNamedStreamExceptionRetainsAvailableCode(t *testing.T) {
	result := bedrockIteratorFailure(t, &btypes.ThrottlingException{Message: aws.String("Too many requests, please wait.")}, bedrockFailureRequestID)
	assertBedrockFailureDetails(t, result, `{"errorCode":"ThrottlingException","requestId":"`+bedrockFailureRequestID+`"}`)
	if result.ErrorMessage != "Throttling error: Too many requests, please wait." {
		t.Fatalf("error=%q", result.ErrorMessage)
	}
}

func TestBedrockDiagnosticValueUTF16Bounds(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"empty", " \t\r\n ", ""}, {"BOM trim", "\ufeff value \ufeff", "value"}, {"NEL is not JS whitespace", "\u0085id\u0085", "\u0085id\u0085"},
		{"200 BMP units", strings.Repeat("界", 200), strings.Repeat("界", 200)},
		{"201 BMP units", strings.Repeat("界", 201), ""},
		{"200 astral units", strings.Repeat("😀", 100), strings.Repeat("😀", 100)},
		{"202 astral units", strings.Repeat("😀", 101), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeBedrockDiagnosticValue(tc.input); got != tc.want {
				t.Fatalf("value=%q want %q", got, tc.want)
			}
		})
	}
}
