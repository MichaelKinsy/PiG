// Package angrypigs is the PiG Standard /angry-pigs game: launch pigs from a
// slingshot to knock birds off their towers. It draws a half-block pixel-art
// scene over the whole terminal through the remote-component API of an
// ordinary Go extension Resource.
package angrypigs

import (
	"fmt"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/termgame"
)

func Extension() *sdk.Extension {
	ext := sdk.New("angrypigs")
	ext.Command("angry-pigs", "Play Angry Pigs: launch pigs at the birds.", run)
	return ext
}

func run(ctx sdk.Context, _ string) error {
	highScore := loadHighScore(ctx.ConfigHome())
	game := newComponent(highScore, standardlogin.ActiveVariant(ctx.ConfigHome()), ctx.Height)
	result, err := ctx.Custom(game, termgame.Overlay("Angry Pigs"))
	if err != nil {
		return err
	}
	score, highScore := scores(result, game.State())
	if err := saveHighScore(ctx.ConfigHome(), highScore); err != nil {
		return err
	}
	ctx.Notify(fmt.Sprintf("Angry Pigs score %d · high %d", score, highScore), "info")
	return nil
}

func scores(result any, fallback gameState) (score, highScore int) {
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
