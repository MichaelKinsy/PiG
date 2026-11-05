package v4a

import "testing"

func TestRedactSigningLogHidesTheCredentialHeaderValues(t *testing.T) {
	canonical := "POST\n/model/x/converse\n\nhost:bedrock.example\nx-amz-date:20260101T000000Z\nx-amz-security-token:SESSION-TOKEN\n\nhost;x-amz-date;x-amz-security-token\nabc"
	want := "POST\n/model/x/converse\n\nhost:bedrock.example\nx-amz-date:20260101T000000Z\nx-amz-security-token:<redacted>\n\nhost;x-amz-date;x-amz-security-token\nabc"
	if got := redactSigningLog(canonical); got != want {
		t.Fatalf("redactSigningLog = %q, want %q", got, want)
	}
	if got := redactSigningLog("host:h\nx-amz-date:d"); got != "host:h\nx-amz-date:d" {
		t.Fatalf("a request with no credential header changed: %q", got)
	}
}
