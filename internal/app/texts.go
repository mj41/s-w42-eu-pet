package app

import (
	"embed"
	"fmt"
	"math/rand/v2"
	"strings"
)

// What the pet says: lines/<lang>.txt (one file per language, easy to read and edit).
// Spoken by the voice (voice.go) and shown in the speech bubble, which folds the
// Czech letters for the robot's font (asciiOnly).
//
//	[key]          when it is said; each line below is one variant (picked at random)
//	[food <food>]  what it says about a food
//	[names]        food = name, for the %s in [eat]
//	# comment
//
//go:embed lines/*.txt
var linesFS embed.FS

var (
	robotTexts = map[string]map[string][]string{} // lang -> key -> variants
	foodTexts  = map[string]map[string][]string{} // lang -> food -> variants
	foodNames  = map[string]map[string]string{}   // lang -> food -> name
)

func init() {
	files, _ := linesFS.ReadDir("lines")
	for _, f := range files {
		b, _ := linesFS.ReadFile("lines/" + f.Name())
		if err := parseLines(strings.TrimSuffix(f.Name(), ".txt"), string(b)); err != nil {
			panic("lines/" + f.Name() + ": " + err.Error())
		}
	}
}

// parseLines reads one language's lines file.
func parseLines(lang, text string) error {
	robotTexts[lang], foodTexts[lang], foodNames[lang] = map[string][]string{}, map[string][]string{}, map[string]string{}
	section := ""
	for n, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case line == "" || strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			section = strings.TrimSpace(line[1 : len(line)-1])
		case section == "":
			return fmt.Errorf("line %d: text before the first [section]", n+1)
		case section == "names":
			food, name, ok := strings.Cut(line, "=")
			if !ok {
				return fmt.Errorf("line %d: want food = name", n+1)
			}
			foodNames[lang][strings.TrimSpace(food)] = strings.TrimSpace(name)
		case strings.HasPrefix(section, "food "):
			food := strings.TrimSpace(strings.TrimPrefix(section, "food "))
			foodTexts[lang][food] = append(foodTexts[lang][food], line)
		default:
			robotTexts[lang][section] = append(robotTexts[lang][section], line)
		}
	}
	return nil
}

// foodText picks what the pet says about a food.
func foodText(lang, food string) string {
	variants := foodTexts[lang][food]
	if len(variants) == 0 {
		variants = foodTexts["en"][food]
	}
	if len(variants) == 0 {
		return text(lang, "eat", foodNames[lang][food])
	}
	return variants[rand.IntN(len(variants))]
}

// text picks a variant for key in lang, with %s replaced by arg.
func text(lang, key, arg string) string {
	variants := robotTexts[lang][key]
	if len(variants) == 0 {
		variants = robotTexts["en"][key]
	}
	if len(variants) == 0 {
		return ""
	}
	t := variants[rand.IntN(len(variants))]
	if strings.HasPrefix(t, "%s") && arg != "" { // capitalize a leading food name
		first := []rune(arg)
		arg = strings.ToUpper(string(first[:1])) + string(first[1:])
	}
	return strings.ReplaceAll(t, "%s", arg)
}

var czechFold = strings.NewReplacer(
	"á", "a", "č", "c", "ď", "d", "é", "e", "ě", "e", "í", "i", "ň", "n", "ó", "o", "ř", "r", "š", "s", "ť", "t", "ú", "u", "ů", "u", "ý", "y", "ž", "z",
	"Á", "A", "Č", "C", "Ď", "D", "É", "E", "Ě", "E", "Í", "I", "Ň", "N", "Ó", "O", "Ř", "R", "Š", "S", "Ť", "T", "Ú", "U", "Ů", "U", "Ý", "Y", "Ž", "Z",
)

// asciiOnly folds Czech letters and drops anything else the robot cannot draw.
func asciiOnly(s string) string {
	s = czechFold.Replace(s)
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7E {
			return -1
		}
		return r
	}, s)
}
