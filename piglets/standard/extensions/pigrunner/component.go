package pigrunner

import (
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	standardlogin "github.com/MichaelKinsy/PiG/piglets/standard/extensions/piglogin"
	"github.com/MichaelKinsy/PiG/piglets/standard/internal/termgame"
)

type runnerState struct {
	Score     int  `json:"score"`
	HighScore int  `json:"highScore"`
	GameOver  bool `json:"gameOver"`
}

// runnerComponent runs PiG Runner as a full-terminal remote component. A
// fixed 60 Hz step advances the game off the TUI loop; the SDK coalesces the
// resulting invalidations to its frame interval.
type runnerComponent struct {
	mu         sync.Mutex
	game       *Game
	variant    standardlogin.Variant
	height     func() int
	lastHeight int
	buffers    frameBuffers
	invalidate func()
	stop       chan struct{}
	stopped    chan struct{}
	stopOnce   sync.Once
}

func newRunnerComponent(highScore int, variant standardlogin.Variant, height func() int) *runnerComponent {
	game := NewGameWithVariant(80, highScore, variant)
	game.Waiting = true
	component := &runnerComponent{
		game:    game,
		variant: variant,
		height:  height,
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
	go component.run()
	return component
}

func (c *runnerComponent) run() {
	defer close(c.stopped)
	ticker := time.NewTicker(tickRate)
	defer ticker.Stop()
	last := time.Now()
	var carry time.Duration
	for {
		select {
		case now := <-ticker.C:
			// Step one Dino frame per elapsed frame interval, so a late
			// tick does not slow the game down.
			carry += min(now.Sub(last), 6*tickRate)
			last = now
			height := c.height()
			c.mu.Lock()
			changed := height != c.lastHeight
			for ; carry >= tickRate; carry -= tickRate {
				changed = c.game.Update() || changed
			}
			c.lastHeight = height
			invalidate := c.invalidate
			c.mu.Unlock()
			if changed && invalidate != nil {
				invalidate()
			}
		case <-c.stop:
			return
		}
	}
}

// Render fills the overlay: the playfield above the two HUD lines, or the
// playfield alone when the overlay is too short for both.
func (c *runnerComponent) Render(width int) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, h := termgame.Viewport(width, c.height())
	c.game.Resize(w)
	hud := h >= 1+hudLines
	playfield := h
	if hud {
		playfield -= hudLines
	}
	return c.game.RenderLines(&c.buffers, w, playfield*2, hud, make([]string, 0, h))
}

func (c *runnerComponent) HandleInput(data string) (sdk.RemoteComponentResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key, text := termgame.ParseKey(data)
	if c.game.Waiting {
		switch {
		case key == termgame.KeyEscape || key == termgame.KeyText && (text == 'q' || text == 'Q'):
			return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}, nil
		case key == termgame.KeyUp || key == termgame.KeyEnter || key == termgame.KeyText && (text == ' ' || text == 'w' || text == 'W'):
			c.game.Start()
		}
		return sdk.RemoteComponentResult{}, nil
	}
	switch {
	case key == termgame.KeyEscape || key == termgame.KeyText && (text == 'q' || text == 'Q'):
		return sdk.RemoteComponentResult{Done: true, Value: c.stateLocked()}, nil
	case key == termgame.KeyUp || key == termgame.KeyText && (text == ' ' || text == 'w' || text == 'W'):
		if !c.game.Over {
			c.game.Jump()
		}
	case key == termgame.KeyDown || key == termgame.KeyText && (text == 's' || text == 'S'):
		if !c.game.Over {
			c.game.Duck()
		}
	case key == termgame.KeyText && (text == 'p' || text == 'P'):
		if !c.game.Over {
			c.game.TogglePause()
		}
	case key == termgame.KeyText && (text == 'r' || text == 'R'):
		if c.game.Over {
			c.game = NewGameWithVariant(c.game.Width, c.game.HighScore, c.variant)
		}
	}
	return sdk.RemoteComponentResult{}, nil
}

func (c *runnerComponent) SetInvalidate(invalidate func()) {
	c.mu.Lock()
	c.invalidate = invalidate
	c.mu.Unlock()
}

func (c *runnerComponent) Dispose() {
	c.stopOnce.Do(func() { close(c.stop) })
	<-c.stopped
}

func (c *runnerComponent) State() runnerState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stateLocked()
}

func (c *runnerComponent) stateLocked() runnerState {
	return runnerState{
		Score:     c.game.Score,
		HighScore: max(c.game.HighScore, c.game.Score),
		GameOver:  c.game.Over,
	}
}
