package pigrunner

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/termgame"
)

func Extension() *sdk.Extension {
	ext := sdk.New("pigrunner")
	ext.Command("runner", "Open PiG Runner.", run)
	ext.Command("pig-runner", "Open PiG Runner.", run)
	return ext
}

func run(ctx sdk.Context, _ string) error {
	highScore := loadHighScore(ctx.ConfigHome())
	component := newRunnerComponent(highScore, standardlogin.ActiveVariant(ctx.ConfigHome()), ctx.Height)
	result, err := ctx.Custom(component, termgame.Overlay("PiG Runner"))
	if err != nil {
		return err
	}

	score, highScore := runnerScores(result, component.State())
	if err := saveHighScore(ctx.ConfigHome(), highScore); err != nil {
		return err
	}
	ctx.Notify(fmt.Sprintf("PiG Runner score %d · high %d", score, highScore), "info")
	return nil
}

func runnerScores(result any, fallback runnerState) (score, highScore int) {
	score, highScore = fallback.Score, fallback.HighScore
	values, ok := result.(map[string]any)
	if !ok {
		return score, highScore
	}
	if value, ok := values["score"].(float64); ok && value >= 0 {
		score = int(value)
	}
	if value, ok := values["highScore"].(float64); ok && value >= 0 {
		highScore = int(value)
	}
	return score, max(highScore, score)
}
