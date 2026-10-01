# stackchan-pet

A Tamagotchi for [Stack-chan](https://github.com/m5stack/StackChan) robots
running Embody Mode. The pet lives on this server; the robot is its body.
Kids play with the robot itself and with a picture page on a phone or tablet.
Parents set the daily routine behind a PIN.

It is a separate server that the robot switches to from its QR screen
(Next, then Connect). It speaks the Embody Mode protocol from
[stackchan-server](https://github.com/mj41/stackchan-server) (`wire` package).

## Playing

| On the robot | The pet |
|---|---|
| Stroke or press the head | Cuddle: happy face, heart, a chirp |
| Hold an NFC card to it | Eats the card's food (shown big on the screen) |
| Tap the screen | Shows its needs as three bars for a few seconds |
| Hold a finger on the screen | A game of catch (below) |
| Shake it | "Wheee!", dizzy |
| Come close (proximity) | Says hello now and then |

The kid's page (`http://<server>:8770/` after scanning the robot's QR code)
works with pictures, so it needs no reading: the pet's face, three need bars
(food, fun, energy) and four big buttons: food, cuddle, play and nap. Actions
from the robot show on the page too.

**Catch the ball** (the play button, or a long press on the robot's screen):
the robot's screen shows a ball in one of its four quarters, and the kid taps
it on the robot. Five balls, about five seconds each; a tap on the wrong
quarter is just ignored, and a ball not caught flies on to the next place.
The head makes each ball harder: ball 1 it keeps still, ball 2 it circles,
ball 3 it moves at random, and balls 4 and 5 it dodges a hand that comes
close (seen by the light sensor next to the screen: proximity rises or the
hand's shadow darkens the light, streamed at 20 samples/s with `light_stream`;
each dodge adds a second). Every catch chirps and blinks green; at the end the
robot shows one star per catch and says the score. More catches, more fun. The page shows the stars as
they come. Without the robot connected, play is a short dance on the page.

Needs: food, fun and energy go from 0 to 100 and drop while the pet is awake.
Its mood follows them (happy, fine, hungry, bored, tired). The pet is kind: it
never dies or gets ill; neglected it only gets sad and asks for food or play
now and then.

## Parent page

`/parent` asks for a PIN; the first PIN entered becomes the PIN. Settings:

- name, language (Czech or English), difficulty (how fast needs drop)
- wake-up and bedtime for weekdays and weekends; optional school hours
- minutes of play per day (feeding is never limited)
- sounds and volume, night light, screen off at night
- food cards: tags seen by the robot, each assigned a food
- the needs themselves: sliders for food, fun and energy, or all full
- demo mode: a need at 90% drops back to 10% (a reset picture and a line on the
  robot), and a nap is quick: +20% energy in 5 seconds, 75% after a minute,
  then the pet wakes up by itself; the next nap passes 90% within seconds and
  the demo reset ends it (back to 10%)
- the game: seconds per ball (3-10) and whether the head moves (circles, dodges)
- a drawn face on the robot: colourful pictures per mood (`internal/robotpic/faces/`)
  instead of the robot's own blinking face; they step aside for speech bubbles
- a log of what happened, previews of the morning and bedtime routines and of every sound

The daily routine: 10 minutes before bedtime the pet yawns. At bedtime it
plays a lullaby, says good night, dims the screen and turns on a warm night
light that fades out within 10 minutes; the screen goes off 5 minutes after it
was last lit (a touch at night lights it again, dimmed, for another 5). At night a light touch on the head (one finger, or a tap on the screen) gets a
sleepy "Zzz..." or, now and then, a dream on the robot's screen, silently; the whole palm on the head (all three
touch zones at full, 3,3,3) wakes the pet quietly for a few minutes (parent
setting, 0 = never), then it falls asleep again with the lullaby. In the morning it
wakes the screen, greets and plays a tune. Needs pause at night and during
school hours, so the pet never suffers while the kid sleeps or is away.

The firmware lets go of the head servos at rest, so the head slowly sinks; by
day the pet lifts it back (from the robot's telemetry) to where it last put it.
At night and during naps it may droop.

## Running

```sh
go run ./cmd/stackchan-pet -tz Europe/Prague
```

| Flag | Default | |
|---|---|---|
| `-listen` | `:8770` | robots and browsers |
| `-public-url` | `http://<LAN IP>:8770` | base of the QR link |
| `-token-file` | `~/.config/stackchan-server/robot-token` | robot bearer token, generated if missing |
| `-state-file` | `~/.local/state/stackchan-pet/state.json` | pets, PINs and pairings; `""` = memory only |
| `-tz` | this machine's | the family's time zone for the schedule |
| `-ui-dir` | | development: serve the pages from disk (`internal/app/ui`) |

The default token file is stackchan-server's, so that server can offer the pet
to its robots:

```sh
stackchan-server -offer Pet=ws://192.168.1.10:8770,$HOME/.config/stackchan-server/robot-token
```

The robot adds the offer to its server list. On its QR screen, press Next
until "Pet" shows, then Connect, and scan the new QR code with the kid's phone.

## Layout

- `internal/pet`: the model (needs, schedule, moods, actions), no I/O
- `internal/app`: robot connections, the engine (robot events → pet actions,
  moods → face, speech, LEDs, sounds), HTTP API, pages in `ui/`
- `internal/sound`: synthesized sounds, 16 kHz PCM, each under the robot's 3 s buffer
- `internal/robotpic`: 320x240 JPEG pictures for the robot's screen (needs, food)

The pet speaks its lines: Microsoft Edge's read-aloud voice (Czech: Antonín, a
little higher and faster, English: Ana), made a bit robot by ffmpeg (40% of a
robotized copy mixed in). Each line is made once and kept in `-voice-dir`; at
start the pet makes all its fixed lines ahead of time. Without Edge (offline) it
falls back to espeak-ng. The speech is streamed to the robot at speaking pace.
The robot's speech bubble font has no Czech letters: the bubble shows the
lines without diacritics.

The pet on the pages is a tiny Stack-chan (`internal/app/ui/chan/`, drawn for
this project after the robot's own face). Stack-chan is developed and
published by meganetaaan, https://github.com/meganetaaan/stack-chan; the
character is used under its
[derivative work guideline](https://github.com/rt-net/stack-chan/blob/main/GUIDELINE.md),
which asks for that credit where users see it (the pages show it).

Other pictures are [Fluent Emoji Flat](https://github.com/microsoft/fluentui-emoji)
(MIT, `internal/app/ui/emoji/LICENSE`); `internal/robotpic/render-icons.sh`
renders the PNGs for the robot's screen.

The pet keeps its pictures in the robot's file store (folder `pet/`): at
connect it asks for the robot's file list and uploads what is missing or
changed (compared by CRC-32), so later it can show them without sending them
again.

## Development

```sh
gofmt -l . && go vet ./... && go test -race ./...
```

`go.mod` points `github.com/mj41/stackchan-server` at `../stackchan-server`
(a `replace`) until that module is published with the public `wire` package.
