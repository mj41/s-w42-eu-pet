// Command genfaces writes the robot's full-screen faces (../faces/*.svg) from the kid page's
// Fluent Emoji (../../app/ui/emoji, MIT, © Microsoft): the round head is dropped, its colour
// fills the whole 4:3 screen, and the eyes and mouth are cropped to fill it. render-icons.sh
// runs it before rendering the faces to JPEG.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// face: the emoji it comes from and the part of its 32x32 box shown (4:3, x y width).
type face struct {
	emoji   string
	x, y, w float64
}

var faces = map[string]face{
	"happy":    {"happy", 2, 5.5, 28},
	"neutral":  {"neutral", 2, 4.5, 28},
	"sad":      {"sad", 0.5, 4, 31}, // the tear on the left
	"grumpy":   {"grumpy", 2, 6.5, 28},
	"sleeping": {"sleeping", -0.5, 1.5, 33}, // the Zzz at the top right
	"yawn":     {"yawn", 2, 7.5, 28},
	"yum":      {"yum", 2, 4.5, 28},
}

var (
	firstPath = regexp.MustCompile(`(?s)<path [^>]*fill="(#[0-9A-Fa-f]{6})"\s*/>`)
	svgOpen   = regexp.MustCompile(`(?s)<svg[^>]*>`)
)

func main() {
	dir := filepath.Dir(os.Args[0])
	if len(os.Args) > 1 {
		dir = os.Args[1] // the robotpic directory
	}
	for name, f := range faces {
		src, err := os.ReadFile(filepath.Join(dir, "..", "app", "ui", "emoji", f.emoji+".svg"))
		if err != nil {
			fail(err)
		}
		s := string(src)
		head := firstPath.FindStringSubmatchIndex(s)
		if head == nil {
			fail(fmt.Errorf("%s: no head path", f.emoji))
		}
		skin := s[head[2]:head[3]]
		body := s[head[1]:] // everything after the head: eyes, mouth, extras
		body = strings.TrimSuffix(strings.TrimSpace(body), "</svg>")
		defs := ""
		if m := svgOpen.FindStringIndex(s); m != nil {
			defs = strings.TrimSpace(s[m[1]:head[0]]) // gradients before the head, if any
		}
		h := f.w * 3 / 4
		out := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="320" height="240" viewBox="%g %g %g %g">
<!-- From Fluent Emoji Flat (MIT, © Microsoft), %s.svg: made by genfaces, do not edit. -->
<defs><radialGradient id="skin" cx="50%%" cy="45%%" r="75%%"><stop offset="0" stop-color="%s" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".18"/></radialGradient></defs>
%s
<rect x="%g" y="%g" width="%g" height="%g" fill="%s"/>
<rect x="%g" y="%g" width="%g" height="%g" fill="url(#skin)"/>
%s
</svg>
`, f.x, f.y, f.w, h, f.emoji, skin, defs, f.x, f.y, f.w, h, skin, f.x, f.y, f.w, h, strings.TrimSpace(body))
		if err := os.WriteFile(filepath.Join(dir, "faces", name+".svg"), []byte(out), 0o644); err != nil {
			fail(err)
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "genfaces:", err)
	os.Exit(1)
}
