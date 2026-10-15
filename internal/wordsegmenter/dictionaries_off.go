//go:build pig_strip_word_dictionaries

package wordsegmenter

import "github.com/MichaelKinsy/PiG/internal/pigstrip"

// pig additive (D92): a Piglet Binary built with pig_strip_word_dictionaries embeds no word dictionary. Each run of CJK, Thai, Lao, Khmer or Burmese text is one word, as ICU segments it without dictionary data.

func init() { pigstrip.Strip(pigstrip.ListFeatures, pigstrip.WordDictionaries) }

func cjkBoundaries(string) []int { return nil }

func seaDivide(string, rune, func(int)) {}
