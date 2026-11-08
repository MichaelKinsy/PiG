package voice

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

// voiceServerFiles are the files of Pi's apps/p2/voice-server at the vendored commit (c23b9ec1ef12a63573d739d4c294f81a377b7d57),
// by SHA-256: the speech service (Python, CPU only) the owner runs beside p2 and names in voice.json. p2 launches none of it;
// it is carried exactly as Pi ships it. test_segmenter.py is Pi's own test of segmenter.py, run in the service's image (it needs
// sherpa-onnx and the speech models: `docker run --rm p2-voice python test_segmenter.py`).
var voiceServerFiles = map[string]string{
	"Dockerfile":           "d2458933238875a43e697ceb6cb70304c7b200e748856ef70cfcf16d90aa7d9d",
	"engines.py":           "da24537dd5b3f8caed074f7c9a7687515c5c0542361de43215b23938a4ed8a20",
	"segmenter.py":         "a1ab4f3eccae3df89520cc136b75be1a94e2798c5fb3c2ae31cde6f6373b6ae2",
	"server.py":            "afaa3041056701a1bcd9ad2eb2ca8fbfab4ecd9b6bd4377db71727a40b912ff9",
	"supertonic_helper.py": "568616a1dcbd42f80b751fdcb801d57dbe7e0956ed774a1fb8498e7760e2bdfc",
	"test_segmenter.py":    "e915f05d84180707fdc9e82ac474c636bdc69e97af5067ebeac6308414253d81",
}

func TestVoiceServerIsPisVerbatim(t *testing.T) {
	directory := filepath.Join("..", "..", "voice-server")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	// test_segmenter_stub.py is PiG's (make voice-segmenter-test), not Pi's: every other entry is.
	pigEntries := 0
	for _, entry := range entries {
		if entry.Name() == "test_segmenter_stub.py" {
			pigEntries++
		}
	}
	if len(entries)-pigEntries != len(voiceServerFiles) {
		t.Errorf("voice-server holds %d entries of Pi's, Pi's holds %d", len(entries)-pigEntries, len(voiceServerFiles))
	}
	for name, want := range voiceServerFiles {
		content, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Error(err)
			continue
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s is %s, Pi's is %s", name, got, want)
		}
	}
}
