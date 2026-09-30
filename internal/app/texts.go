package app

import (
	"math/rand/v2"
	"strings"
)

// What the pet says, spoken (espeak-ng, voice.go) and shown in the speech bubble.
// The robot's font has no Czech letters: the bubble gets them folded (asciiOnly).
// Several variants: one is picked at random so the pet does not sound like a machine.
var robotTexts = map[string]map[string][]string{
	"cs": {
		"eat":         {"Mňam, %s!", "%s! To je dobrota!", "Mňam mňam!"},
		"full":        {"Už nemůžu, jsem plný!", "Děkuju, už mám dost."},
		"cuddle":      {"To je příjemné!", "Ještě!", "Mrrr...", "Mám tě rád!"},
		"play":        {"Hurá, hrajeme!", "Juchů!", "To je zábava!"},
		"too_tired":   {"Jsem moc unavený...", "Nejdřív si odpočinu."},
		"limit":       {"Dnes už jsme si hráli dost. Zítra zas!", "Už si odpočinu. Zítra!"},
		"nap":         {"Jdu si zdřímnout...", "Chvilku si zdřímnu."},
		"not_tired":   {"Nejsem unavený!", "Spát? Teď ne!"},
		"wake":        {"Už jsem vzhůru!", "Dobře jsem se vyspal!"},
		"shake":       {"Jůůů!", "Točí se mi hlava!"},
		"hello":       {"Ahoj!", "Ahoj, rád tě vidím!"},
		"hungry":      {"Mám hlad!", "Mám hlad! Dal bych si něco dobrého."},
		"bored":       {"Pojď si hrát!", "Nudím se..."},
		"tired":       {"Jsem ospalý...", "Chtěl bych si zdřímnout."},
		"bedtime":     {"Za chvíli půjdu spát.", "Už se mi chce spát..."},
		"goodnight":   {"Dobrou noc!", "Dobrou noc, sladké sny!"},
		"morning":     {"Dobré ráno!", "Dobré ráno! Mám hlad!"},
		"game":        {"Chyť míček!", "Hrajeme! Chyť míček!"},
		"night_wake":  {"Ááá... už jsem vzhůru.", "Co je? Já jsem spal..."},
		"game_over":   {"Hurá! %s!", "Super, %s!"},
		"game_over_0": {"Příště to vyjde!", "Zkusíme to znovu?"},
	},
	"en": {
		"eat":         {"Yum, %s!", "%s! Delicious!", "Nom nom!"},
		"full":        {"I'm full!", "Thanks, that's enough."},
		"cuddle":      {"That's nice!", "More!", "Purr...", "I love you!"},
		"play":        {"Yay, let's play!", "Wheee!", "So much fun!"},
		"too_tired":   {"I'm too tired...", "I need a rest first."},
		"limit":       {"Enough play for today. Tomorrow!", "Time to rest. Tomorrow!"},
		"nap":         {"Time for a nap...", "Just a little nap."},
		"not_tired":   {"I'm not tired!", "Sleep? Not now!"},
		"wake":        {"I'm awake!", "What a nice nap!"},
		"shake":       {"Wheee!", "I'm dizzy!"},
		"hello":       {"Hi!", "Hi, nice to see you!"},
		"hungry":      {"I'm hungry!", "Something yummy, please?"},
		"bored":       {"Let's play!", "I'm bored..."},
		"tired":       {"I'm sleepy...", "I'd like a nap."},
		"bedtime":     {"Bedtime soon.", "I'm getting sleepy..."},
		"goodnight":   {"Good night!", "Good night, sweet dreams!"},
		"morning":     {"Good morning!", "Good morning! I'm hungry!"},
		"game":        {"Catch the ball!", "Let's play catch!"},
		"night_wake":  {"Yaaawn... I'm awake.", "What is it? I was sleeping..."},
		"game_over":   {"Yay! %s!", "Great, %s!"},
		"game_over_0": {"Next time!", "Let's try again?"},
	},
}

// foodTexts: what the pet says about each food.
var foodTexts = map[string]map[string][]string{
	"cs": {
		"apple":  {"Křup křup! Jablíčko!", "Jablíčko je zdravíčko!", "Mňam, jablíčko!"},
		"carrot": {"Mrkvička! Teď uvidím i potmě!", "Křupy křup, mrkvička!", "Jsem zajíček? Mňam!"},
		"banana": {"Banán! Opičky by mi záviděly!", "Mňam, banán! Ú ú á á!", "Žlutý a sladký!"},
		"bread":  {"Chlebíček! Křupavá kůrčička!", "Mňam, chlebíček!", "Takový dobrý chlebík!"},
		"milk":   {"Mlíčko! Teď mám bílý knírek!", "Glo glo glo... mňam!", "Mlíčko pro silné roboty!"},
		"cake":   {"Dortík! Mám dnes narozeniny?", "Sladké! Ještě kousek?", "Mňam! Dortík je nejlepší!"},
	},
	"en": {
		"apple":  {"Crunch crunch! Apple!", "An apple a day!", "Yum, apple!"},
		"carrot": {"Carrot! Now I can see in the dark!", "Crunchy carrot!", "Am I a bunny? Yum!"},
		"banana": {"Banana! The monkeys are jealous!", "Yum, banana! Ooh-ooh-aah!", "Yellow and sweet!"},
		"bread":  {"Bread! Crunchy crust!", "Yum, bread!", "Such good bread!"},
		"milk":   {"Milk! Now I have a white moustache!", "Glug glug glug... yum!", "Milk for strong robots!"},
		"cake":   {"Cake! Is it my birthday?", "Sweet! One more piece?", "Yum! Cake is the best!"},
	},
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

// foodNames for the general eat line ("Mnam, jablicko!"), for foods without their own lines.
var foodNames = map[string]map[string]string{
	"cs": {"apple": "jablíčko", "carrot": "mrkvička", "banana": "banán", "bread": "chlebíček", "milk": "mlíčko", "cake": "dortík"},
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
