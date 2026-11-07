package surfaces

import (
	"os"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// Ports packages/chord/src/surfaces/format.ts (numbers). Pi's formatNumber is `Intl.NumberFormat` with the current
// language's locale; outside a render the platform's default locale applies. Go has no current language (D450): the
// language is the first parameter, nil outside a render.
//
// FormatTime is not ported: Go has no CLDR date patterns (golang.org/x/text has no date formatting), and a hand table
// of a few locales would be a different function. It stays `pending` in the ledger (rows/chord-ui.md).

// NumberOptions are the `Intl.NumberFormatOptions` FormatNumber honors: the fraction digits. Nil fields keep Intl's
// defaults (0 to 3 fraction digits).
type NumberOptions struct {
	MinimumFractionDigits *int
	MaximumFractionDigits *int
}

// FormatNumber is value in language's locale (`1.234,5` in German); nil language: the platform's default locale.
func FormatNumber(lang *Language, value float64, options *NumberOptions) string {
	tag := platformLocale()
	if lang != nil {
		tag = language.Make(lang.Locale)
	}
	maxDigits, minDigits := 3, 0
	if options != nil {
		if options.MaximumFractionDigits != nil {
			maxDigits = *options.MaximumFractionDigits
		}
		if options.MinimumFractionDigits != nil {
			minDigits = *options.MinimumFractionDigits
			maxDigits = max(maxDigits, minDigits)
		}
	}
	return message.NewPrinter(tag).Sprint(number.Decimal(value, number.MinFractionDigits(minDigits), number.MaxFractionDigits(maxDigits)))
}

// platformLocale is the platform's default locale (`Intl`'s): the first of LC_ALL, LC_MESSAGES, LANG that is a tag,
// else en-US.
func platformLocale() language.Tag {
	for _, name := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		value, _, _ := strings.Cut(os.Getenv(name), ".")
		if tag, err := language.Parse(strings.ReplaceAll(value, "_", "-")); err == nil {
			return tag
		}
	}
	return language.AmericanEnglish
}
