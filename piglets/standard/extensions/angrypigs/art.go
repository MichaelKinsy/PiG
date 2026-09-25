package angrypigs

import (
	"image/color"

	"github.com/MichaelKinsy/PiG/piglets/standard/internal/pixel"
)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: 0xFF} }

// Scene colors.
var (
	bandColor    = rgb(0x3B, 0x22, 0x14)
	dustColor    = rgb(0xE9, 0xDF, 0xC9)
	puffColor    = rgb(0xFF, 0xFF, 0xFF)
	trailColor   = rgb(0xFF, 0xFF, 0xFF)
	previewDot   = rgb(0xFF, 0xFF, 0xFF)
	hudInk       = rgb(0xFF, 0xFF, 0xFF)
	hudShadow    = rgb(0x1B, 0x3A, 0x55)
	hudGold      = rgb(0xFF, 0xD8, 0x4D)
	featherColor = rgb(0xF4, 0xC4, 0x30)
	meterFrame   = rgb(0x1B, 0x14, 0x10)
	meterEmpty   = rgb(0x3A, 0x30, 0x2A)
	meterLow     = rgb(0x5E, 0xD0, 0x6B)
	meterMid     = rgb(0xFF, 0xD8, 0x4D)
	meterHigh    = rgb(0xF0, 0x4E, 0x3A)
)

// Block materials: a 6x6 texture per material and its palette. 'h' is the lit
// top edge, 'd' the shaded border that separates neighboring blocks.
var (
	woodTexture = []string{
		"hhhhhd",
		"wwwwwd",
		"gggwwd",
		"wwwwwd",
		"wwgggd",
		"dddddd",
	}
	stoneTexture = []string{
		"hhhhhd",
		"ssmssd",
		"ssmssd",
		"mmmmmd",
		"sssmsd",
		"dddddd",
	}
	iceTexture = []string{
		"hhhhhd",
		"hiiicd",
		"ihiicd",
		"iihicd",
		"ccccid",
		"dddddd",
	}
	woodPalette = pixel.NewPalette(map[byte]color.RGBA{
		'h': rgb(0xE8, 0xAE, 0x68), 'w': rgb(0xC9, 0x8A, 0x4B), 'g': rgb(0xA1, 0x68, 0x35), 'd': rgb(0x6B, 0x42, 0x22),
	})
	stonePalette = pixel.NewPalette(map[byte]color.RGBA{
		'h': rgb(0xC2, 0xC8, 0xD0), 's': rgb(0x9A, 0xA0, 0xA9), 'm': rgb(0x74, 0x7A, 0x83), 'd': rgb(0x52, 0x57, 0x5E),
	})
	icePalette = pixel.NewPalette(map[byte]color.RGBA{
		'h': rgb(0xFA, 0xFE, 0xFF), 'i': rgb(0xBF, 0xEC, 0xFB), 'c': rgb(0x96, 0xD7, 0xF0), 'd': rgb(0x5E, 0xB0, 0xD6),
	})
)

// Crack overlays for blocks that took one and two hits of damage.
var cracks = [...][]string{
	nil,
	{
		"......",
		"...x..",
		"..x...",
		"..xx..",
		"......",
		"......",
	},
	{
		"....x.",
		".x.x..",
		"..x...",
		".xx.x.",
		"x...x.",
		"......",
	},
}

func materialColor(kind cellKind) color.RGBA {
	switch kind {
	case cellWood:
		return woodPalette['w']
	case cellStone:
		return stonePalette['s']
	case cellIce:
		return icePalette['i']
	}
	return featherColor
}

// birdFrames are a 6x6 bird facing the slingshot, eyes open and blinking.
// 'Y' body, 'y' belly, 'O' beak, 'W' eye white, 'K' pupil and brow, 'k'
// crest and tail.
var birdFrames = [2][]string{
	{
		"..kk..",
		".KKYYk",
		"YWKYYY",
		"OOYYyY",
		".YyyYY",
		"..YYY.",
	},
	{
		"..kk..",
		".KKYYk",
		"YKKYYY",
		"OOYYyY",
		".YyyYY",
		"..YYY.",
	},
}

var birdPalette = pixel.NewPalette(map[byte]color.RGBA{
	'Y': rgb(0xF4, 0xC4, 0x30), 'y': rgb(0xFF, 0xE9, 0x9A), 'O': rgb(0xF0, 0x7A, 0x1E),
	'W': rgb(0xFF, 0xFF, 0xFF), 'K': rgb(0x1F, 0x17, 0x14), 'k': rgb(0xC9, 0x8F, 0x12),
})

// pigBall is the 8x8 launched pig, drawn with the login sprite's palette so
// it matches the selected pig.
var pigBall = []string{
	".OO..OO.",
	"OeeOOeeO",
	"OPPPPPPO",
	"OWKPPKWO",
	"ObPssPbO",
	"OPsKKsPO",
	".OPPPPO.",
	"..OOOO..",
}

// slingshot is the 9x17 wooden fork. 'T' is lit wood, 't' shaded wood, 'b'
// the rubber band's anchor.
var slingshot = []string{
	"bTT...TTb",
	".Tt...Tt.",
	".TT...TT.",
	".tT...Tt.",
	".TT...TT.",
	"..TT.TT..",
	"..tTTTt..",
	"...TTT...",
	"...tTt...",
	"...TTT...",
	"...TTt...",
	"...TTT...",
	"...tTT...",
	"...TTT...",
	"...TTt...",
	"..tTTTt..",
	".tttttt..",
}

var slingPalette = pixel.NewPalette(map[byte]color.RGBA{
	'T': rgb(0xB0, 0x74, 0x3C), 't': rgb(0x80, 0x50, 0x28), 'b': bandColor,
})
