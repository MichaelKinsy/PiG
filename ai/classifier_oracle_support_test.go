package ai

import (
	"fmt"
	"math/rand"
	"strings"
)

// drawClassifierState draws the JSON text of a classifier state: nested values with the strings a Go string cannot hold (a lone surrogate), numbers JSON.stringify
// writes in its own forms, and member names in the order both a JavaScript object and a Go map write them (ascending bytes, no integer-like name).
func drawClassifierState(rng *rand.Rand) string {
	pick := func(items ...string) string { return items[rng.Intn(len(items))] }
	var value func(depth int) string
	value = func(depth int) string {
		switch n := rng.Intn(9); {
		case n < 3 || depth > 2:
			return pick(`"alpha"`, `""`, `"line1\nline2"`, `"\ud83d"`, `"x\ud83d\ude00y"`, `"\u2028<&>"`, `"é"`, `"tab\t"`)
		case n == 3:
			return pick(`0`, `-0`, `1.5`, `1e21`, `1e-7`, `123456789012345680000`, `-3`, `0.1`)
		case n == 4:
			return pick(`true`, `false`, `null`)
		case n < 7:
			var items []string
			for range rng.Intn(4) {
				items = append(items, value(depth+1))
			}
			return "[" + strings.Join(items, ",") + "]"
		default:
			var members []string
			for _, key := range []string{"a", "b", "c", "k", "z", "é"} {
				if rng.Intn(3) == 0 {
					members = append(members, fmt.Sprintf("%q:%s", key, value(depth+1)))
				}
			}
			return "{" + strings.Join(members, ",") + "}"
		}
	}
	var members []string
	for _, key := range []string{"a", "b", "c", "nested"} {
		if rng.Intn(2) == 0 {
			members = append(members, fmt.Sprintf("%q:%s", key, value(0)))
		}
	}
	return "{" + strings.Join(members, ",") + "}"
}

// drawClassifierQuestions draws one to four questions with ids and choice keys of every JavaScript key class (words, integer-like names that an object lists first), choice option counts around the limits, score
// level counts around the limits and empty instructions or descriptions.
func drawClassifierQuestions(rng *rand.Rand) []llamaOracleQuestion {
	words := []string{"Is it urgent?", "Pick one \u2028 <&>", "line1\nline2", "", "😀", "Rate it", " padded "}
	idPool := []string{"team", "urgent", "2", "10", "b", "a", "é", "severity", "1"}
	keyPool := []string{"billing", "technical", "2", "10", "z", "a", "sales", "1", "é", "b"}
	var questions []llamaOracleQuestion
	used := map[string]bool{}
	for range 1 + rng.Intn(4) {
		id := idPool[rng.Intn(len(idPool))]
		if used[id] {
			continue
		}
		used[id] = true
		question := llamaOracleQuestion{ID: id, Instructions: words[rng.Intn(len(words))]}
		switch rng.Intn(3) {
		case 0:
			question.Type = "choice"
			count := []int{0, 1, 2, 2, 3, 3, 4, 5, 6}[rng.Intn(9)]
			if rng.Intn(60) == 0 {
				count = 63
			}
			seen := map[string]bool{}
			for i := range count {
				key := keyPool[rng.Intn(len(keyPool))]
				if count == 63 {
					key = fmt.Sprintf("key%d", i)
				}
				if seen[key] {
					continue
				}
				seen[key] = true
				question.Criteria = append(question.Criteria, llamaOracleChoice{Key: key, Description: words[rng.Intn(len(words))]})
			}
		case 1:
			question.Type = "score"
			question.Levels = []string{}
			for range []int{0, 1, 2, 3, 3, 5, 10, 11}[rng.Intn(8)] {
				question.Levels = append(question.Levels, words[rng.Intn(len(words))])
			}
		default:
			question.Type = "bool"
			question.Yes, question.No = words[rng.Intn(len(words))], words[rng.Intn(len(words))]
		}
		questions = append(questions, question)
	}
	return questions
}

// classifierQuestionsOf is the ClassifierQuestions a probe's question specs describe.
func classifierQuestionsOf(specs []llamaOracleQuestion) ClassifierQuestions {
	var questions ClassifierQuestions
	for _, q := range specs {
		switch q.Type {
		case "choice":
			criteria := []ClassifierChoiceCriterion{}
			for _, c := range q.Criteria {
				criteria = append(criteria, ClassifierChoiceCriterion(c))
			}
			questions = append(questions, ClassifierQuestionEntry{ID: q.ID, Question: ClassifierChoiceQuestion{Instructions: q.Instructions, Criteria: criteria}})
		case "score":
			questions = append(questions, ClassifierQuestionEntry{ID: q.ID, Question: ClassifierScoreQuestion{Instructions: q.Instructions, Criteria: q.Levels}})
		default:
			questions = append(questions, ClassifierQuestionEntry{ID: q.ID, Question: ClassifierBoolQuestion{Instructions: q.Instructions, Criteria: ClassifierBoolCriteria{True: q.Yes, False: q.No}}})
		}
	}
	return questions
}
