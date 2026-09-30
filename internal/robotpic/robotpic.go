// Package robotpic draws pictures for the robot's 320x240 screen (JPEG, sent
// as wire.BinShowJPEG): the pet's needs as bars, and a big food while it eats.
// Pictures, not text: the kid may not read yet, and the robot's fonts have no
// Czech letters. The icons are Fluent Emoji Flat (MIT), rendered to PNG by
// render-icons.sh.
package robotpic

import (
	"bytes"
	"embed"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"sync"
)

//go:embed png/*.png
var pngFS embed.FS

// Screen size of the robot.
const W, H = 320, 240

var (
	iconsMu sync.Mutex
	icons   = map[string]image.Image{}
)

// icon loads png/<name>.png (160 px), nil if missing.
func icon(name string) image.Image {
	iconsMu.Lock()
	defer iconsMu.Unlock()
	if img, ok := icons[name]; ok {
		return img
	}
	var img image.Image
	if b, err := pngFS.ReadFile("png/" + name + ".png"); err == nil {
		img, _ = png.Decode(bytes.NewReader(b))
	}
	icons[name] = img
	return img
}

var (
	cream  = color.RGBA{0xFF, 0xF7, 0xE6, 0xFF}
	track  = color.RGBA{0xE8, 0xDF, 0xCC, 0xFF}
	green  = color.RGBA{0x4C, 0xAF, 0x50, 0xFF}
	yellow = color.RGBA{0xF5, 0xB7, 0x00, 0xFF}
	red    = color.RGBA{0xE5, 0x39, 0x35, 0xFF}
)

// Needs shows food, fun and energy (0..100) as three icon + bar rows.
func Needs(food, fun, energy float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{cream}, image.Point{}, draw.Src)
	rows := []struct {
		icon  string
		value float64
	}{{"apple", food}, {"heart", fun}, {"battery", energy}}
	for i, r := range rows {
		y := 14 + i*76
		if ic := icon(r.icon); ic != nil {
			drawScaled(img, ic, image.Rect(14, y, 14+64, y+64))
		}
		bar := image.Rect(96, y+12, 304, y+52)
		fillRound(img, bar, track)
		c := green
		switch {
		case r.value < 30:
			c = red
		case r.value < 60:
			c = yellow
		}
		fill := bar
		fill.Max.X = bar.Min.X + max(bar.Dy(), int(float64(bar.Dx())*max(0, min(100, r.value))/100))
		fillRound(img, fill, c)
	}
	return encode(img)
}

// Food shows one food big, centered on a warm background.
func Food(name string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{cream}, image.Point{}, draw.Src)
	if ic := icon(name); ic != nil {
		drawScaled(img, ic, image.Rect(W/2-90, H/2-90, W/2+90, H/2+90))
	}
	return encode(img)
}

func encode(img image.Image) []byte {
	var b bytes.Buffer
	jpeg.Encode(&b, img, &jpeg.Options{Quality: 80})
	return b.Bytes()
}

// drawScaled draws src into r (area sampling, then alpha blending over dst).
func drawScaled(dst *image.RGBA, src image.Image, r image.Rectangle) {
	sb := src.Bounds()
	sx := float64(sb.Dx()) / float64(r.Dx())
	sy := float64(sb.Dy()) / float64(r.Dy())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			// average the source pixels this destination pixel covers
			x0, x1 := sb.Min.X+int(float64(x-r.Min.X)*sx), sb.Min.X+int(float64(x-r.Min.X+1)*sx)
			y0, y1 := sb.Min.Y+int(float64(y-r.Min.Y)*sy), sb.Min.Y+int(float64(y-r.Min.Y+1)*sy)
			x1, y1 = max(x1, x0+1), max(y1, y0+1)
			var rs, gs, bs, as, n uint32
			for yy := y0; yy < y1; yy++ {
				for xx := x0; xx < x1; xx++ {
					cr, cg, cb, ca := src.At(xx, yy).RGBA() // premultiplied
					rs, gs, bs, as, n = rs+cr, gs+cg, bs+cb, as+ca, n+1
				}
			}
			rs, gs, bs, as = rs/n, gs/n, bs/n, as/n
			d := dst.RGBAAt(x, y)
			inv := 0xFFFF - as
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((rs + uint32(d.R)*0x101*inv/0xFFFF) >> 8),
				G: uint8((gs + uint32(d.G)*0x101*inv/0xFFFF) >> 8),
				B: uint8((bs + uint32(d.B)*0x101*inv/0xFFFF) >> 8),
				A: 0xFF,
			})
		}
	}
}

// fillRound fills r with fully rounded ends (radius = half the height).
func fillRound(dst *image.RGBA, r image.Rectangle, c color.RGBA) {
	rad := float64(r.Dy()) / 2
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cx := min(max(float64(x)+0.5, float64(r.Min.X)+rad), float64(r.Max.X)-rad)
			cy := float64(r.Min.Y) + rad
			dx, dy := float64(x)+0.5-cx, float64(y)+0.5-cy
			if dx*dx+dy*dy <= rad*rad {
				dst.SetRGBA(x, y, c)
			}
		}
	}
}
