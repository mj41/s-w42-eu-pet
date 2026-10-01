// Package robotpic draws pictures for the robot's 320x240 screen (JPEG, sent
// as wire.BinShowJPEG): the pet's needs as bars, and a big food while it eats.
// Pictures, not text: the kid may not read yet, and the robot's fonts have no
// Czech letters. The icons are Fluent Emoji Flat (MIT), rendered to PNG by
// render-icons.sh.
package robotpic

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"sync"
)

//go:embed png
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

// Needs shows food, fun and energy (0..100) as three icon + bar rows, with room at the
// bottom for the close button (the menu places it).
func Needs(food, fun, energy float64) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{cream}, image.Point{}, draw.Src)
	rows := []struct {
		icon  string
		value float64
	}{{"apple", food}, {"heart", fun}, {"zzz", energy}}
	for i, r := range rows {
		y := 6 + i*62
		if ic := icon(r.icon); ic != nil {
			drawScaled(img, ic, image.Rect(20, y, 20+54, y+54))
		}
		bar := image.Rect(96, y+10, 304, y+44)
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
			d := dst.RGBAAt(x, y) // premultiplied too: "over" keeps a transparent dst transparent
			inv := 0xFFFF - as
			dst.SetRGBA(x, y, color.RGBA{
				R: uint8((rs + uint32(d.R)*0x101*inv/0xFFFF) >> 8),
				G: uint8((gs + uint32(d.G)*0x101*inv/0xFFFF) >> 8),
				B: uint8((bs + uint32(d.B)*0x101*inv/0xFFFF) >> 8),
				A: uint8((as + uint32(d.A)*0x101*inv/0xFFFF) >> 8),
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

// Spots is the number of places the ball can be: the four quarters of the screen.
const Spots = 4

// SpotAt is the quarter of the screen a tap at x, y lands in (0 top left, 1 top right,
// 2 bottom left, 3 bottom right).
func SpotAt(x, y float64) int {
	s := 0
	if x >= W/2 {
		s++
	}
	if y >= H/2 {
		s += 2
	}
	return s
}

// Ball shows the ball in one quarter of the screen: tap it!
func Ball(spot int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{cream}, image.Point{}, draw.Src)
	cx, cy := W/4+(spot%2)*W/2, H/4+(spot/2%2)*H/2
	if ic := icon("ball"); ic != nil {
		drawScaled(img, ic, image.Rect(cx-52, cy-52, cx+52, cy+52))
	}
	return encode(img)
}

// Stars shows the result of a game: one star per catch, faded ones for misses.
func Stars(hits, rounds int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{cream}, image.Point{}, draw.Src)
	star := icon("star")
	if star == nil || rounds <= 0 {
		return encode(img)
	}
	size := min(64, (W-16)/rounds-4)
	x := (W - rounds*(size+4)) / 2
	for i := range rounds {
		ic := star
		if i >= hits {
			ic = faded(star)
		}
		drawScaled(img, ic, image.Rect(x, H/2-size/2, x+size, H/2+size/2))
		x += size + 4
	}
	return encode(img)
}

// faded is a grey, mostly transparent copy of an icon (a star not earned).
func faded(src image.Image) image.Image {
	b := src.Bounds()
	out := image.NewNRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
			g := uint8((uint32(c.R)*3 + uint32(c.G)*6 + uint32(c.B)) / 10)
			out.SetNRGBA(x, y, color.NRGBA{g * 3 / 4, g * 3 / 4, g * 3 / 4, c.A / 2})
		}
	}
	return out
}

// Dreams are what the sleeping pet may dream of (icons in png/).
var Dreams = []string{"apple", "cake", "banana", "ball", "heart", "star", "milk", "carrot"}

var (
	night = color.RGBA{0x0e, 0x14, 0x33, 0xFF}
	cloud = color.RGBA{0xe8, 0xec, 0xff, 0xFF}
)

// Dream shows the night sky with a dream cloud: what the pet dreams of.
func Dream(item string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{night}, image.Point{}, draw.Src)
	if moon := icon("moon"); moon != nil {
		drawScaled(img, moon, image.Rect(18, 18, 18+64, 18+64))
	}
	if star := icon("star"); star != nil {
		for _, p := range [][3]int{{110, 24, 18}, {40, 178, 14}, {92, 120, 12}, {292, 24, 14}} {
			drawScaled(img, star, image.Rect(p[0], p[1], p[0]+p[2], p[1]+p[2]))
		}
	}
	// The dream cloud: a puff of circles, with small ones leading to it from the moon.
	for _, c := range [][3]int{{205, 120, 62}, {160, 138, 42}, {250, 140, 44}, {185, 90, 40}, {232, 88, 40}, {205, 162, 40}, {104, 88, 9}, {124, 104, 13}} {
		fillCircle(img, c[0], c[1], c[2], cloud)
	}
	if ic := icon(item); ic != nil {
		drawScaled(img, ic, image.Rect(205-50, 125-50, 205+50, 125+50))
	}
	return encode(img)
}

func fillCircle(dst *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy <= r*r && image.Pt(x, y).In(dst.Bounds()) {
				dst.SetRGBA(x, y, c)
			}
		}
	}
}

// CloudPNG is a transparent dream cloud (200x150) for a sprite next to the sleeping face.
func CloudPNG() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 200, 150))
	for _, c := range [][3]int{{110, 70, 52}, {72, 84, 36}, {150, 88, 38}, {92, 46, 34}, {132, 44, 34}, {110, 104, 34}, {26, 128, 8}, {42, 112, 12}} {
		fillCircle(img, c[0], c[1], c[2], cloud)
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// Files are the pictures for the robot's file store (name -> bytes): the icons (PNG),
// the full-screen faces (face-<mood>.jpg) and the dream cloud.
func Files() map[string][]byte {
	out := map[string][]byte{"cloud.png": CloudPNG(), "menu-bg.png": menuBackdrop()}
	for _, t := range menuTilesList() {
		out[t.name+".png"] = menuTile(t)
	}
	for _, c := range Colors {
		out["color-"+c+".png"] = ColorButton(c)
	}
	for pct := 0; pct <= 100; pct += 10 {
		out[fmt.Sprintf("zbar-%d.png", pct)] = EnergyBar(pct)
	}
	entries, _ := pngFS.ReadDir("png")
	for _, e := range entries {
		if b, err := pngFS.ReadFile("png/" + e.Name()); err == nil {
			out[e.Name()] = b
		}
	}
	return out
}

// Menu pictures for the robot's screen (sprites; the server places and reads them):
// rounded coloured tiles with a big icon (transparent corners), per menu size.
type tile struct {
	name, icon string // icon: an emoji PNG, or "back" / "needs" drawn
	color      color.RGBA
	w, h       int
}

var (
	tileOrange = color.RGBA{0xff, 0xa7, 0x26, 0xff}
	tileGreen  = color.RGBA{0x4c, 0xaf, 0x50, 0xff}
	tilePurple = color.RGBA{0x7e, 0x57, 0xc2, 0xff}
	tilePink   = color.RGBA{0xec, 0x40, 0x7a, 0xff}
	tileGrey   = color.RGBA{0x78, 0x90, 0x9c, 0xff}
	tileCream  = color.RGBA{0xff, 0xf3, 0xe0, 0xff}
	tileBlue   = color.RGBA{0x29, 0x79, 0xff, 0xff}
)

// Tile sizes of the menus (the server lays them out on the 320x240 screen).
const (
	MainTile  = 84 // main menu: square
	FoodTileW = 92 // food menu
	FoodTileH = 76
	PlayTileW = 96 // play menu (three tiles)
	PlayTileH = 110
	BackTileW = 120 // the back button at the bottom
	BackTileH = 44
)

func menuTilesList() []tile {
	t := []tile{
		{"menu-food", "plate", tileOrange, MainTile, MainTile},
		{"menu-play", "ball", tileGreen, MainTile, MainTile},
		{"menu-nap", "moon", tilePurple, MainTile, MainTile},
		{"menu-needs", "needs", tilePink, MainTile, MainTile},
		{"menu-back", "back", tileGrey, BackTileW, BackTileH},
		{"menu-catch", "ball", tileGreen, PlayTileW, PlayTileH},
		{"menu-dance", "party", tileBlue, PlayTileW, PlayTileH},
		{"menu-colors", "colors", tileCream, PlayTileW, PlayTileH},
	}
	for _, f := range []string{"apple", "carrot", "banana", "bread", "milk", "cake"} {
		t = append(t, tile{"menu-food-" + f, f, tileCream, FoodTileW, FoodTileH})
	}
	return t
}

// menuTile draws a tile: rounded, coloured, with the icon as big as fits.
func menuTile(t tile) []byte {
	img := image.NewRGBA(image.Rect(0, 0, t.w, t.h))
	fillRoundRect(img, img.Bounds(), min(18, t.h/3), t.color)
	cx, cy := t.w/2, t.h/2
	white := color.RGBA{0xff, 0xff, 0xff, 0xff}
	switch t.icon {
	case "colors": // the color game: its six colors as dots, three by two
		for i, name := range Colors {
			fillCircle(img, cx+(i%3-1)*t.w*3/10, cy+(i/3*2-1)*t.h/5, t.w/8, ColorRGBA[name])
		}
	case "needs": // the needs picture in small: three icons with their bars
		k := func(v int) int { return v * t.h / 96 } // drawn for 96 px
		for i, n := range []struct {
			icon  string
			share float64
			c     color.RGBA
		}{{"apple", 1, green}, {"heart", 0.6, yellow}, {"zzz", 0.3, red}} {
			y := k(10 + i*27)
			if ic := icon(n.icon); ic != nil {
				drawScaled(img, ic, image.Rect(k(8), y, k(8+24), y+k(24)))
			}
			bar := image.Rect(k(38), y+k(6), k(88), y+k(18))
			fillRound(img, bar, white)
			fill := bar
			fill.Max.X = bar.Min.X + max(bar.Dy(), int(float64(bar.Dx())*n.share))
			fillRound(img, fill, n.c)
		}
	case "back": // an arrow pointing left
		for i := -22; i <= 22; i++ {
			fillCircle(img, cx+i, cy, 4, white)
		}
		for i := 0; i <= 12; i++ {
			fillCircle(img, cx-22+i, cy-i, 4, white)
			fillCircle(img, cx-22+i, cy+i, 4, white)
		}
	default:
		if ic := icon(t.icon); ic != nil {
			s := min(t.w, t.h) * 3 / 4
			drawScaled(img, ic, image.Rect(cx-s/2, cy-s/2, cx+s/2, cy+s/2))
		}
	}
	return encodePNG(img)
}

// menuBackdrop dims the face behind the menu.
func menuBackdrop() []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{0x0e, 0x14, 0x33, 0xc8}}, image.Point{}, draw.Src)
	return encodePNG(img)
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// fillRoundRect fills r with rounded corners of radius rad.
func fillRoundRect(dst *image.RGBA, r image.Rectangle, rad int, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			cx := min(max(x, r.Min.X+rad), r.Max.X-1-rad)
			cy := min(max(y, r.Min.Y+rad), r.Max.Y-1-rad)
			if dx, dy := x-cx, y-cy; dx*dx+dy*dy <= rad*rad {
				dst.SetRGBA(x, y, c)
			}
		}
	}
}

// needIcon is a need's icon (as on the needs picture).
var needIcon = map[string]string{"food": "apple", "fun": "heart", "energy": "zzz"}

// DemoReset shows that demo mode reset a need: the refresh arrows, the need's icon and
// its bar back down at 10%.
func DemoReset(need string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	draw.Draw(img, img.Bounds(), &image.Uniform{night}, image.Point{}, draw.Src)
	if ic := icon(needIcon[need]); ic != nil {
		drawScaled(img, ic, image.Rect(W/2-60, 30, W/2+60, 150))
	}
	if ic := icon("refresh"); ic != nil { // a badge: "reset"
		drawScaled(img, ic, image.Rect(W/2+40, 14, W/2+104, 78))
	}
	bar := image.Rect(40, 182, 280, 218)
	fillRound(img, bar, color.RGBA{0x3a, 0x40, 0x6a, 0xff})
	low := bar
	low.Max.X = bar.Min.X + max(bar.Dy(), bar.Dx()/10)
	fillRound(img, low, red)
	return encode(img)
}

// The color game's colors (the robot's LEDs show the same).
var (
	Colors    = []string{"red", "yellow", "green", "white", "blue", "purple"} // white, not cyan: on the LEDs cyan looked like blue
	ColorRGBA = map[string]color.RGBA{                                        // the screen's colors; the LEDs show ColorLED
		"red":    {0xe5, 0x39, 0x35, 0xff},
		"yellow": {0xfd, 0xd8, 0x35, 0xff},
		"green":  {0x43, 0xa0, 0x47, 0xff},
		"white":  {0xf5, 0xf5, 0xf5, 0xff},
		"blue":   {0x1e, 0x5b, 0xe5, 0xff},
		"purple": {0x9c, 0x27, 0xb0, 0xff},
	}
	// ColorLED is each color for the robot's LEDs (ledColor).
	ColorLED = map[string]string{}
)

// Color button size: six fit on the screen, three by two.
const ColorButtonW, ColorButtonH = 96, 104

// ColorButton is one big rounded button of a color (PNG, white rim).
func ColorButton(name string) []byte {
	img := image.NewRGBA(image.Rect(0, 0, ColorButtonW, ColorButtonH))
	rim := color.RGBA{0xff, 0xff, 0xff, 0xff}
	if name == "white" {
		rim = color.RGBA{0x9e, 0x9e, 0x9e, 0xff}
	}
	fillRoundRect(img, img.Bounds(), 18, rim)
	fillRoundRect(img, image.Rect(5, 5, ColorButtonW-5, ColorButtonH-5), 14, ColorRGBA[name])
	return encodePNG(img)
}

// EnergyBar is the tiny energy bar in the sleeping face's corner (PNG): a bar filled
// to pct percent, colored like the needs bars (the server shows it faint).
const EnergyBarW, EnergyBarH = 80, 10

func EnergyBar(pct int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, EnergyBarW, EnergyBarH))
	bar := img.Bounds()
	fillRound(img, bar, color.RGBA{0x55, 0x5c, 0x7a, 0xff})
	c := green
	switch {
	case pct < 30:
		c = red
	case pct < 60:
		c = yellow
	}
	fill := bar
	fill.Max.X = bar.Min.X + max(bar.Dy(), bar.Dx()*max(0, min(100, pct))/100)
	fillRound(img, fill, c)
	return encodePNG(img)
}

// LED color tuning: the LEDs are linear (the screen's colors are gamma encoded), their
// green is much brighter than red and blue, and at full power the diffuser washes the
// colors out to pastel. Every color gets the same power (its strongest channel).
const (
	ledGamma = 2.2
	ledGreen = 0.65 // the green channel's share
	ledLevel = 0.7  // the strongest channel, of full
)

// ledColor is the screen color c for the LEDs, as "#rrggbb".
func ledColor(c color.RGBA) string {
	lin := func(v uint8) float64 { return math.Pow(float64(v)/255, ledGamma) }
	r, g, b := lin(c.R), lin(c.G)*ledGreen, lin(c.B)
	k := 255 * ledLevel / max(r, g, b, 1e-6)
	ch := func(v float64) int { return int(math.Round(v * k)) }
	return fmt.Sprintf("#%02x%02x%02x", ch(r), ch(g), ch(b))
}

func init() {
	for name, c := range ColorRGBA {
		ColorLED[name] = ledColor(c)
	}
}
