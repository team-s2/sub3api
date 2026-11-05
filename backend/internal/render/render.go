// Package render draws the bot's quota overview and cost flow images.
//
// The layouts are ported from the former Python renderer and keep its
// geometry, palette and typography. Drawing is pure Go on golang.org/x/image.
package render

import "errors"

// Color is 0xAARRGGBB.
type Color uint32

// alpha returns the color with its alpha replaced.
func (c Color) alpha(a uint8) Color { return c&0x00FFFFFF | Color(a)<<24 }

// Weight selects one of the two Noto Sans CJK SC weights the design uses.
type Weight int

const (
	Regular Weight = iota
	Bold
)

// path is a closed outline of lines and cubic curves, filled with the
// non-zero winding rule.
type path struct {
	verbs  []pathVerb
	points []float64
}

type pathVerb uint8

const (
	verbMove pathVerb = iota
	verbLine
	verbCubic
	verbClose
)

func (p *path) moveTo(x, y float64) {
	p.verbs = append(p.verbs, verbMove)
	p.points = append(p.points, x, y)
}

func (p *path) lineTo(x, y float64) {
	p.verbs = append(p.verbs, verbLine)
	p.points = append(p.points, x, y)
}

func (p *path) cubicTo(x1, y1, x2, y2, x3, y3 float64) {
	p.verbs = append(p.verbs, verbCubic)
	p.points = append(p.points, x1, y1, x2, y2, x3, y3)
}

func (p *path) close() { p.verbs = append(p.verbs, verbClose) }

// ErrTooLarge reports an image beyond the size the chat clients accept.
var ErrTooLarge = errors.New("image too large")
