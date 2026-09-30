package app

import (
	"math/rand/v2"
	"strings"
)

// Speech bubble texts. The robot's font has no Czech letters, so the Czech
// texts are written without diacritics (sayText strips any that slip in).
// Several variants: one is picked at random so the pet does not sound like a machine.
var robotTexts = map[string]map[string][]string{
	"cs": {
		"asleep":    {"Psst... spim.", "Zzz... dobrou noc.", "Ted se spi. Zitra!"},
		"eat":       {"Mnam, %s!", "%s! To je dobrota!", "Mnam mnam!"},
		"full":      {"Uz nemuzu, jsem plny!", "Dekuju, uz mam dost."},
		"cuddle":    {"To je prijemne!", "Jeste!", "Mrrr...", "Mam te rad!"},
		"play":      {"Hura, hrajeme!", "Juchuu!", "To je zabava!"},
		"too_tired": {"Jsem moc unaveny...", "Nejdriv si odpocinu."},
		"limit":     {"Dnes uz jsme si hrali dost. Zitra zas!", "Uz si odpocinu. Zitra!"},
		"nap":       {"Jdu si zdrimnout...", "Chvilku si zdrimnu."},
		"not_tired": {"Nejsem unaveny!", "Spat? Ted ne!"},
		"wake":      {"Uz jsem vzhuru!", "Dobre jsem se vyspal!"},
		"shake":     {"Juuu!", "Toci se mi hlava!"},
		"hello":     {"Ahoj!", "Ahoj, rad te vidim!"},
		"hungry":    {"Mam hlad!", "Dal bych si neco dobreho."},
		"bored":     {"Pojd si hrat!", "Nudim se..."},
		"tired":     {"Jsem ospaly...", "Chtel bych si zdrimnout."},
		"bedtime":   {"Za chvili pujdu spat.", "Uz se mi chce spat..."},
		"goodnight": {"Dobrou noc!", "Dobrou noc, sladke sny!"},
		"morning":   {"Dobre rano!", "Dobre rano! Mam hlad!"},
	},
	"en": {
		"asleep":    {"Shh... sleeping.", "Zzz... good night.", "It's sleep time. Tomorrow!"},
		"eat":       {"Yum, %s!", "%s! Delicious!", "Nom nom!"},
		"full":      {"I'm full!", "Thanks, that's enough."},
		"cuddle":    {"That's nice!", "More!", "Purr...", "I love you!"},
		"play":      {"Yay, let's play!", "Wheee!", "So much fun!"},
		"too_tired": {"I'm too tired...", "I need a rest first."},
		"limit":     {"Enough play for today. Tomorrow!", "Time to rest. Tomorrow!"},
		"nap":       {"Time for a nap...", "Just a little nap."},
		"not_tired": {"I'm not tired!", "Sleep? Not now!"},
		"wake":      {"I'm awake!", "What a nice nap!"},
		"shake":     {"Wheee!", "I'm dizzy!"},
		"hello":     {"Hi!", "Hi, nice to see you!"},
		"hungry":    {"I'm hungry!", "Something yummy, please?"},
		"bored":     {"Let's play!", "I'm bored..."},
		"tired":     {"I'm sleepy...", "I'd like a nap."},
		"bedtime":   {"Bedtime soon.", "I'm getting sleepy..."},
		"goodnight": {"Good night!", "Good night, sweet dreams!"},
		"morning":   {"Good morning!", "Good morning! I'm hungry!"},
	},
}

// foodNames for the speech bubble ("Mnam, jablicko!").
var foodNames = map[string]map[string]string{
	"cs": {"apple": "jablicko", "carrot": "mrkvicka", "banana": "banan", "bread": "chlebicek", "milk": "mlicko", "cake": "dortik"},
	"en": {"apple": "apple", "carrot": "carrot", "banana": "banana", "bread": "bread", "milk": "milk", "cake": "cake"},
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
		arg = strings.ToUpper(arg[:1]) + arg[1:]
	}
	return asciiOnly(strings.ReplaceAll(t, "%s", arg))
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
