package ai

import "testing"

// Pi packages/ai/src/utils/estimate.ts estimateTextAndImageContentTokens: ceil(chars/3.5), image = 4800 chars, string length in UTF-16 units.
func TestEstimateTextAndImageContentTokensLikeUpstream(t *testing.T) {
	cases := []struct {
		name    string
		content UserContent
		want    int
	}{
		{"empty text", UserText(""), 0},
		{"five chars rounds up", UserText("hello"), 2},
		{"seven chars", UserText("abcdefg"), 2},
		{"eight chars", UserText("abcdefgh"), 3},
		{"astral char is two UTF-16 units", UserText("😀"), 1},
		{"image only", UserContentBlocks{ImageContent{}}, 1372},
		{"text plus image", UserContentBlocks{TextContent{Text: "abcd"}, ImageContent{}}, 1373},
		{"empty blocks", UserContentBlocks{}, 0},
	}
	for _, tc := range cases {
		if got := EstimateTextAndImageContentTokens(tc.content); got != tc.want {
			t.Errorf("%s: got %d, want %d", tc.name, got, tc.want)
		}
		if got := EstimateMessageTokens(UserMessage{Content: tc.content}); got != tc.want {
			t.Errorf("%s via EstimateMessageTokens: got %d, want %d", tc.name, got, tc.want)
		}
	}
}
