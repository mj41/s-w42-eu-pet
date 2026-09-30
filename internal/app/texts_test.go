package app

import (
	"strings"
	"testing"

	"github.com/mj41/stackchan-pet/internal/pet"
)

// The keys the code says (text(lang, key, ...)).
var lineKeys = []string{"eat", "full", "cuddle", "play", "too_tired", "limit", "nap", "not_tired", "wake", "shake",
	"hello", "hungry", "bored", "tired", "bedtime", "goodnight", "morning", "game", "night_wake", "game_over", "game_over_0",
	"tickle", "long_cuddle", "scratch"}

func TestLinesFilesAreComplete(t *testing.T) {
	for _, lang := range []string{"cs", "en"} {
		for _, key := range lineKeys {
			if len(robotTexts[lang][key]) == 0 {
				t.Errorf("lines/%s.txt: no [%s]", lang, key)
			}
			for _, v := range robotTexts[lang][key] {
				if strings.Contains(v, "%s") != (key == "eat" && strings.Contains(v, "%s") || key == "game_over") {
					t.Errorf("lines/%s.txt [%s]: %%s where it is not filled in (or missing): %q", lang, key, v)
				}
			}
		}
		for key := range robotTexts[lang] {
			found := false
			for _, k := range lineKeys {
				found = found || k == key
			}
			if !found {
				t.Errorf("lines/%s.txt: unknown section [%s]", lang, key)
			}
		}
		for _, food := range pet.FoodOrder {
			if foodNames[lang][food] == "" {
				t.Errorf("lines/%s.txt [names]: no %s", lang, food)
			}
			if len(foodTexts[lang][food]) == 0 {
				t.Errorf("lines/%s.txt: no [food %s]", lang, food)
			}
		}
	}
}

func TestParseLines(t *testing.T) {
	if err := parseLines("xx", "Ahoj\n"); err == nil {
		t.Error("text before a section should be an error")
	}
	if err := parseLines("xx", "[names]\napple jablko\n"); err == nil {
		t.Error("a name without = should be an error")
	}
	delete(robotTexts, "xx")
	delete(foodTexts, "xx")
	delete(foodNames, "xx")
}
