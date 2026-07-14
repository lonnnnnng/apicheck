package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
)

type point struct {
	x float64
	y float64
}

type gradientStop struct {
	at float64
	c  color.NRGBA
}

func main() {
	out := flag.String("out", "appicon-1024.png", "output PNG path")
	size := flag.Int("size", 1024, "output size in px")
	flag.Parse()
	if *size <= 0 {
		panic("size must be positive")
	}

	img := renderIcon(*size)
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		panic(err)
	}
	file, err := os.Create(*out)
	if err != nil {
		panic(err)
	}
	defer file.Close()
	if err := png.Encode(file, img); err != nil {
		panic(err)
	}
}

func renderIcon(size int) *image.NRGBA {
	const ss = 3
	canvas := image.NewNRGBA(image.Rect(0, 0, size*ss, size*ss))
	scale := float64(size*ss) / 512

	s := func(v float64) float64 { return v * scale }

	// 作者 long: 图标直接生成透明 PNG，避免 macOS Quick Look 把 SVG 栅格化成白色方底。
	drawRoundedRect(canvas, s(56), s(52), s(400), s(400), s(92), []gradientStop{
		{0, rgba(255, 255, 255, 250)},
		{0.55, rgba(247, 252, 255, 244)},
		{1, rgba(232, 242, 247, 238)},
	}, 255)
	drawRoundedRect(canvas, s(70), s(70), s(372), s(372), s(86), []gradientStop{
		{0, rgba(15, 23, 42, 18)},
		{1, rgba(15, 23, 42, 0)},
	}, 80)
	drawRoundedRect(canvas, s(118), s(102), s(276), s(276), s(74), []gradientStop{
		{0, rgba(37, 99, 235, 255)},
		{0.48, rgba(8, 145, 178, 255)},
		{1, rgba(249, 115, 22, 245)},
	}, 255)
	drawRoundedRect(canvas, s(152), s(138), s(208), s(196), s(54), []gradientStop{
		{0, rgba(255, 255, 255, 240)},
		{1, rgba(222, 243, 255, 190)},
	}, 255)

	drawThickLine(canvas, point{s(186), s(244)}, point{s(234), s(244)}, s(25), rgba(37, 99, 235, 255))
	drawThickLine(canvas, point{s(234), s(244)}, point{s(264), s(210)}, s(25), rgba(37, 99, 235, 255))
	drawThickLine(canvas, point{s(330), s(278)}, point{s(282), s(278)}, s(25), rgba(8, 145, 178, 255))
	drawThickLine(canvas, point{s(282), s(278)}, point{s(254), s(312)}, s(25), rgba(8, 145, 178, 255))
	drawCircle(canvas, s(186), s(244), s(25), rgba(37, 99, 235, 255))
	drawCircle(canvas, s(330), s(278), s(25), rgba(8, 145, 178, 255))
	drawCircle(canvas, s(266), s(203), s(22), rgba(249, 115, 22, 255))
	drawCircle(canvas, s(251), s(318), s(22), rgba(16, 185, 129, 255))

	drawThickLine(canvas, point{s(198), s(344)}, point{s(238), s(384)}, s(27), rgba(15, 118, 110, 255))
	drawThickLine(canvas, point{s(238), s(384)}, point{s(326), s(286)}, s(27), rgba(15, 118, 110, 255))
	drawThickLine(canvas, point{s(198), s(344)}, point{s(238), s(384)}, s(10), rgba(255, 255, 255, 190))
	drawThickLine(canvas, point{s(238), s(384)}, point{s(326), s(286)}, s(10), rgba(255, 255, 255, 190))

	return downsample(canvas, ss)
}

func drawRoundedRect(img *image.NRGBA, x, y, w, h, r float64, stops []gradientStop, opacity uint8) {
	minX := maxInt(0, int(math.Floor(x)))
	minY := maxInt(0, int(math.Floor(y)))
	maxX := minInt(img.Bounds().Dx(), int(math.Ceil(x+w)))
	maxY := minInt(img.Bounds().Dy(), int(math.Ceil(y+h)))
	for py := minY; py < maxY; py++ {
		for px := minX; px < maxX; px++ {
			if !insideRoundedRect(float64(px)+0.5, float64(py)+0.5, x, y, w, h, r) {
				continue
			}
			t := ((float64(px)-x)/w + (float64(py)-y)/h) / 2
			c := sampleGradient(stops, t)
			c.A = uint8(int(c.A) * int(opacity) / 255)
			blend(img, px, py, c)
		}
	}
}

func drawQuadratic(img *image.NRGBA, a, b, c point, width float64, col color.NRGBA) {
	prev := a
	for i := 1; i <= 48; i++ {
		t := float64(i) / 48
		mt := 1 - t
		next := point{
			x: mt*mt*a.x + 2*mt*t*b.x + t*t*c.x,
			y: mt*mt*a.y + 2*mt*t*b.y + t*t*c.y,
		}
		drawThickLine(img, prev, next, width, col)
		prev = next
	}
}

func drawThickLine(img *image.NRGBA, a, b point, width float64, col color.NRGBA) {
	radius := width / 2
	minX := maxInt(0, int(math.Floor(math.Min(a.x, b.x)-radius-1)))
	minY := maxInt(0, int(math.Floor(math.Min(a.y, b.y)-radius-1)))
	maxX := minInt(img.Bounds().Dx(), int(math.Ceil(math.Max(a.x, b.x)+radius+1)))
	maxY := minInt(img.Bounds().Dy(), int(math.Ceil(math.Max(a.y, b.y)+radius+1)))
	for py := minY; py < maxY; py++ {
		for px := minX; px < maxX; px++ {
			if distanceToSegment(float64(px)+0.5, float64(py)+0.5, a, b) <= radius {
				blend(img, px, py, col)
			}
		}
	}
}

func drawCircle(img *image.NRGBA, cx, cy, r float64, col color.NRGBA) {
	minX := maxInt(0, int(math.Floor(cx-r-1)))
	minY := maxInt(0, int(math.Floor(cy-r-1)))
	maxX := minInt(img.Bounds().Dx(), int(math.Ceil(cx+r+1)))
	maxY := minInt(img.Bounds().Dy(), int(math.Ceil(cy+r+1)))
	r2 := r * r
	for py := minY; py < maxY; py++ {
		for px := minX; px < maxX; px++ {
			dx := float64(px) + 0.5 - cx
			dy := float64(py) + 0.5 - cy
			if dx*dx+dy*dy <= r2 {
				blend(img, px, py, col)
			}
		}
	}
}

func downsample(src *image.NRGBA, ss int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, src.Bounds().Dx()/ss, src.Bounds().Dy()/ss))
	for y := 0; y < dst.Bounds().Dy(); y++ {
		for x := 0; x < dst.Bounds().Dx(); x++ {
			var r, g, b, a int
			for yy := 0; yy < ss; yy++ {
				for xx := 0; xx < ss; xx++ {
					c := src.NRGBAAt(x*ss+xx, y*ss+yy)
					r += int(c.R)
					g += int(c.G)
					b += int(c.B)
					a += int(c.A)
				}
			}
			n := ss * ss
			dst.SetNRGBA(x, y, color.NRGBA{R: uint8(r / n), G: uint8(g / n), B: uint8(b / n), A: uint8(a / n)})
		}
	}
	return dst
}

func insideRoundedRect(px, py, x, y, w, h, r float64) bool {
	cx := clamp(px, x+r, x+w-r)
	cy := clamp(py, y+r, y+h-r)
	dx := px - cx
	dy := py - cy
	return dx*dx+dy*dy <= r*r
}

func distanceToSegment(px, py float64, a, b point) float64 {
	vx := b.x - a.x
	vy := b.y - a.y
	wx := px - a.x
	wy := py - a.y
	l2 := vx*vx + vy*vy
	if l2 == 0 {
		return math.Hypot(px-a.x, py-a.y)
	}
	t := clamp((wx*vx+wy*vy)/l2, 0, 1)
	projX := a.x + t*vx
	projY := a.y + t*vy
	return math.Hypot(px-projX, py-projY)
}

func sampleGradient(stops []gradientStop, t float64) color.NRGBA {
	t = clamp(t, 0, 1)
	if len(stops) == 0 {
		return rgba(0, 0, 0, 0)
	}
	if t <= stops[0].at {
		return stops[0].c
	}
	for i := 1; i < len(stops); i++ {
		if t > stops[i].at {
			continue
		}
		left := stops[i-1]
		right := stops[i]
		local := (t - left.at) / (right.at - left.at)
		return lerpColor(left.c, right.c, local)
	}
	return stops[len(stops)-1].c
}

func lerpColor(a, b color.NRGBA, t float64) color.NRGBA {
	return color.NRGBA{
		R: uint8(float64(a.R) + (float64(b.R)-float64(a.R))*t),
		G: uint8(float64(a.G) + (float64(b.G)-float64(a.G))*t),
		B: uint8(float64(a.B) + (float64(b.B)-float64(a.B))*t),
		A: uint8(float64(a.A) + (float64(b.A)-float64(a.A))*t),
	}
}

func blend(img *image.NRGBA, x, y int, src color.NRGBA) {
	if src.A == 0 {
		return
	}
	dst := img.NRGBAAt(x, y)
	if src.A == 255 || dst.A == 0 {
		img.SetNRGBA(x, y, src)
		return
	}
	sa := float64(src.A) / 255
	da := float64(dst.A) / 255
	oa := sa + da*(1-sa)
	out := color.NRGBA{
		R: uint8((float64(src.R)*sa + float64(dst.R)*da*(1-sa)) / oa),
		G: uint8((float64(src.G)*sa + float64(dst.G)*da*(1-sa)) / oa),
		B: uint8((float64(src.B)*sa + float64(dst.B)*da*(1-sa)) / oa),
		A: uint8(oa * 255),
	}
	img.SetNRGBA(x, y, out)
}

func rgba(r, g, b, a uint8) color.NRGBA {
	return color.NRGBA{R: r, G: g, B: b, A: a}
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
