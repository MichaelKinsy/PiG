package subprocess

import (
	"image"
	"image/color"
)

type halfBlockDepth int

const (
	halfBlockTrueColor halfBlockDepth = iota
	halfBlock256
)

const (
	halfBlockMaxCols = 400
	halfBlockMaxRows = 200
)

func halfBlockSize(pxWidth, pxHeight, maxCols, maxRows int) (cols, rows int) { return 0, 0 }

func halfBlockRows(img image.Image, cols, rows int, depth halfBlockDepth, background color.Color) []string {
	return nil
}

func halfBlock256Index(r, g, b uint8) uint8 { return 0 }
