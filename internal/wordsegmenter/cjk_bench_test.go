//go:build !pig_strip_word_dictionaries

package wordsegmenter

import (
	"math/rand"
	"runtime"
	"strings"
	"testing"
)

// cjkCorpus is ordinary Chinese and Japanese prose: the dictionary engine sees runs of Han, Hiragana and Katakana mixed with digits and Latin words, as an editing session does.
var cjkCorpus = []string{
	"今天的天气非常好，我们决定一起去公园散步，顺便讨论一下下个月的项目计划和预算安排。",
	"人工智能技术的快速发展正在深刻改变软件开发的方式，程序员需要不断学习新的工具和方法。",
	"这个编辑器支持多种语言的分词，包括中文、日文、泰文、老挝文、高棉文和缅甸文，光标可以按词移动。",
	"經濟發展與環境保護之間的平衡，是當代社會必須面對的重要課題，政府和企業都應該承擔責任。",
	"他在图书馆里整整坐了一个下午，认真阅读了关于中国古代历史和文化的几本经典著作。",
	"请把测试结果发送给负责质量保证的同事，并且在周五之前完成所有未解决问题的修复工作。",
	"北京是中华人民共和国的首都，也是全国的政治、文化和国际交往中心，拥有三千多年的建城史。",
	"我们的服务器在凌晨三点发生了故障，运维团队立即启动应急预案，四十分钟后系统恢复正常运行。",
	"学习一门外语需要长期的坚持和大量的练习，每天花半小时背单词比周末突击十个小时更有效果。",
	"新版本的命令行工具增加了会话分支、上下文压缩以及扩展加载等功能，同时减少了启动时间和内存占用。",
	"春天来了，公园里的樱花盛开，许多游客带着相机和家人朋友一起来赏花拍照，场面十分热闹。",
	"数据库查询的性能取决于索引设计、缓存策略和硬件资源，优化之前必须先测量再下结论。",
	"今日は天気がとても良いので、友達と一緒に公園へ散歩に行って、お弁当を食べることにしました。",
	"人工知能の技術は急速に発展しており、ソフトウェア開発の方法を大きく変えようとしています。",
	"東京は日本の首都であり、世界でも有数の大都市として、多くの企業や大学が集まっています。",
	"この文章エディタは、日本語や中国語の単語単位でカーソルを動かすことができるようになりました。",
	"来週の会議の資料をメールで送っていただけますか。金曜日までに確認して、修正点をお知らせします。",
	"コンピュータープログラミングを学ぶためには、アルゴリズムとデータ構造の基礎をしっかり理解することが大切です。",
	"新しいバージョンのコマンドラインツールでは、セッションの分岐やコンテキストの圧縮が使えます。",
	"私は毎朝六時に起きて、コーヒーを飲みながらニュースを読み、それから電車で会社に向かいます。",
	"図書館で借りた本を返しに行ったら、ちょうど新刊のミステリー小説が入荷していたので借りました。",
	"ソフトウェアのテストは、バグを見つけるだけでなく、仕様の曖昧さを明らかにするためにも役立ちます。",
	"春になると桜が咲き、多くの観光客がカメラを持って花見に訪れ、公園はとても賑やかになります。",
	"データベースの検索性能は、インデックスの設計、キャッシュの戦略、ハードウェアの資源に依存します。",
	"天津和上海都是中国的重要港口城市，每年有数以千万计的货物通过这里运往世界各地。",
	"電子書籍リーダーの普及によって、紙の本を読む機会が減ったと感じる人も少なくありません。",
	"在大型分布式系统中，网络分区、时钟漂移和部分失败是必须在设计阶段就考虑的现实问题。",
	"彼女はスーパーマーケットでトマトとキュウリとレタスを買って、サラダを作りました。",
	"这款手机的电池续航时间长达两天，屏幕分辨率很高，价格也比同类产品便宜不少，所以销量很好。",
	"経済産業省は、中小企業の生産性向上を支援するために、新たな補助金制度を創設すると発表しました。",
}

// cjkLines returns count lines of 30 to 60 characters cut at random positions of the corpus, so the lookups reach many different parts of the dictionary.
func cjkLines(count int) []string {
	var all []rune
	for _, line := range cjkCorpus {
		all = append(all, []rune(line)...)
	}
	random := rand.New(rand.NewSource(7))
	lines := make([]string, count)
	for i := range lines {
		start := random.Intn(len(all) - 60)
		lines[i] = string(all[start : start+30+random.Intn(31)])
	}
	return lines
}

func consume(text string) int {
	n := 0
	for range Segments(text) {
		n++
	}
	return n
}

func BenchmarkCJKSegments(b *testing.B) {
	lines := cjkLines(512)
	b.Run("lines", func(b *testing.B) {
		b.ReportAllocs()
		i := 0
		for b.Loop() {
			consume(lines[i%len(lines)])
			i++
		}
	})
	for _, repeat := range []int{128, 4096} {
		b.Run("repeat"+map[int]string{128: "128", 4096: "4096"}[repeat], func(b *testing.B) {
			text := strings.Repeat(cjkCorpus[0], repeat)
			b.ReportAllocs()
			for b.Loop() {
				consume(text)
			}
		})
	}
}

// BenchmarkCJKEditingSession calls Segments 10,000 times over 2,000 different lines, as a long editing session does, and reports what the session retained: heap in use after a collection, and allocations per call.
func BenchmarkCJKEditingSession(b *testing.B) {
	lines := cjkLines(2000)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	calls := 0
	for b.Loop() {
		for i := range 10000 {
			consume(lines[i%len(lines)])
			calls++
		}
	}
	b.StopTimer()
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(after.Mallocs-before.Mallocs)/float64(calls), "mallocs/call")
	runtime.GC()
	runtime.ReadMemStats(&after)
	b.ReportMetric(float64(int64(after.HeapInuse)-int64(before.HeapInuse)), "heap-inuse-growth-B")
	b.ReportMetric(float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)), "heap-alloc-growth-B")
}
