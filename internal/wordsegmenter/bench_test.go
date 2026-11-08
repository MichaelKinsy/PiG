//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"strings"
	"testing"
)

var benchTexts = map[string]string{
	"ja":  "吾輩は猫である。名前はまだ無い。どこで生れたかとんと見当がつかぬ。何でも薄暗いじめじめした所でニャーニャー泣いていた事だけは記憶している。吾輩はここで始めて人間というものを見た。しかもあとで聞くとそれは書生という人間中で一番獰悪な種族であったそうだ。",
	"zh":  "我们在这里讨论中文分词的性能问题，搜索记录和工具描述都需要按照词典切分。今天天气很好，我们去公园散步吧。中华人民共和国成立于一九四九年。",
	"th":  "ภาษาไทยเป็นภาษาที่มีระดับเสียงของคำแน่นอนหรือวรรณยุกต์เช่นเดียวกับภาษาจีน",
	"lo":  "ພາສາລາວແມ່ນພາສາທາງການຂອງປະເທດລາວແລະເປັນພາສາທີ່ໃຊ້ໃນການສື່ສານຂອງຄົນລາວທົ່ວໄປ",
	"km":  "សួស្តីពិភពលោកភាសាខ្មែរគឺជាភាសាផ្លូវការរបស់ប្រទេសកម្ពុជា",
	"my":  "မြန်မာဘာသာစကားသည်မြန်မာနိုင်ငံ၏ရုံးသုံးဘာသာစကားဖြစ်ပြီးလူအများစုပြောဆိုကြသည်",
	"mix": "Open the file 設定ファイル and check ภาษาไทย then 中文分词 works, 吾輩は猫である。",
}

// BenchmarkSegments measures the dictionary engines through the production entry point on a short and a long text of each script. It is the end-to-end figure for a change to the index (README: Performance).
func BenchmarkSegments(b *testing.B) {
	for _, name := range []string{"ja", "zh", "th", "lo", "km", "my", "mix"} {
		for _, repeat := range []int{1, 8} {
			text := strings.Repeat(benchTexts[name], repeat)
			b.Run(name+"/"+string(rune('0'+repeat)), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(text)))
				for b.Loop() {
					segments := 0
					for range Segments(text) {
						segments++
					}
					if segments == 0 {
						b.Fatal("no segments")
					}
				}
			})
		}
	}
}
