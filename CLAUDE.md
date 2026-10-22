# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

Bytewise is a single-window isometric programming-puzzle game written in Go on Ebitengine.
The player fills `main`/`p1`/`p2` programs with command tiles (F, L, R, J, A, P1, P2) so an
orange robot lights every beacon pad on a tile grid. Module name is `bytewise`; one `main`
package at the root plus the `engine` package. `main.py`, `engine.py` and `test_levels.py`
are the original Python/pygame implementation, kept only for reference. Do not extend them;
new work goes in Go.

## Commands

```bash
go build -o bytewise .                                            # dev build
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bytewise .   # release: static, ~12 MB
./bytewise -scale 1                                               # run (needs a display)
go test ./...                                                     # all tests, headless
go test ./engine -run Solutions                                   # single test
go vet ./... && gofmt -l .
```

Tests never open a window. `engine` tests read `../levels.json`; `main_test.go` builds a
`Game` with `newGame()` and calls its input handlers directly, so keep input handling in
methods that take coordinates/keys as arguments rather than reading Ebitengine state inline.

For a visual check without touching the real desktop: start `Xvfb :77`, run the binary with
`DISPLAY=:77 WAYLAND_DISPLAY= XDG_CONFIG_HOME=<tmp>`, and grab frames with
`xwd -root | convert xwd:- out.png`. Mesa software rendering there is very CPU heavy and
drops synthetic key presses sent faster than roughly 300 ms apart; that is the harness, not
the game.

## Architecture

**`engine/` is pure game rules; `main.go` is everything Ebitengine.** Preserve that split.

- `engine.Level` is parsed from `levels.json` by `ParseLevels`. `World` holds mutable run
  state (robot X/Y/D, lit pads). `World.Step(cmd)` applies one primitive and returns an event
  string (`"move" | "jump" | "turn" | "activate" | "blocked"`). `Interpreter.Next()` walks
  main/p1/p2 one instruction at a time with an explicit call stack and sets `Overflow` when
  `MaxSteps` (1000) or `MaxDepth` (200) is exceeded. `Run()` drives both to completion for
  tests.
- `Game.Update()` is the animation-driven stepper: it calls `interp.Next()` only once the
  current animation finishes, feeds the command to `world.Step()`, and uses the returned event
  string as the animation kind. `robotVisual()` interpolates between the `from`/`to`
  snapshots stored on the anim. Adding a new command means: add it to `engine` (constant,
  `AllCommands`, `World.Step`), then an icon case in `drawIcon`, a colour in `cmdColors`, a
  key in `keyCommands`, and an animation case in `robotVisual`.
- Rendering is at a fixed logical size (`winW, winH = 1200, 760`) returned from `Layout`;
  Ebitengine scales to the window and reports cursor positions in logical coordinates, so
  layout code never sees the scale factor. All shapes are drawn with the `vector` package
  through the small `fillPoly`/`strokePoly`/`fillRoundRect` helpers; text uses `text/v2`
  with the embedded Go fonts (bold for titles and tile labels, regular for body text).
- **Antialiased vector shapes are expensive in Ebitengine (measured: ~100 per frame drops an
  Intel iGPU from 60 to 30 fps).** So the board tiles are cached in two full-size layers
  (behind/in front of the robot, rebuilt only when `boardSig` changes) and every command
  tile and empty slot is rasterised once into `tileCache`. Only small, changing things
  (glow halos, highlight ring, boxes, buttons, overlays) are drawn live. Keep it that way
  when adding UI, and check with `BYTEWISE_DEBUG=1 ./bytewise`, which shows and logs
  fps/tps.
- Direction indices: 0=SE (+x), 1=SW (+y), 2=NW (-x), 3=NE (-y). `engine.DirNames` order
  matches the sprite filenames `robot_{se,sw,nw,ne}.png`.
- Movement rules live only in `World.Step`: Forward needs equal height; Jump needs exactly +1
  up or any drop; Activate toggles (not sets) the pad and is `"blocked"` off a pad.
- `levels.json` and `assets/robot/` are embedded with `go:embed`, so changing them requires
  a rebuild. An `assets/robot/` directory next to the executable or in the working directory
  overrides the embedded sprites at runtime (all four PNGs must be present).
- Persistence: `$XDG_CONFIG_HOME/bytewise/save.json` (via `os.UserConfigDir`), with keys
  `current`, `completed`, `programs` keyed by level index as a string, and optional
  `music_off`/`sfx_off`. Written on every edit, on win, on toggling sound, and after
  `RunGame` returns.
- Audio (`audio.go`) is fully synthesised at startup: `composeMusic` renders a looping piece
  (two passes, second kept, so the echo tail wraps seamlessly) and `makeEffects` renders the
  named effects played by `sound.play`. `newGame(false)` (tests, `-nosound`) returns a
  stub `sound` that never opens the device. Music is started from the first `Update`, not
  the constructor. `audio_test.go` checks levels and the loop seam.

## Levels

`levels.json` is a list; `heights` rows are digit strings (`.` = no tile), `start` is
`[x, y, dir]`, `limits` gives slot counts per program (0/missing hides that program and its
P1/P2 palette tile), `commands` restricts the palette. Levels are referenced by index
everywhere (save file, test solutions), so inserting a level in the middle shifts saved
progress and solutions.

**Every level needs a reference solution in `solutions` in `engine/engine_test.go`, in level
order.** The test asserts the counts match, that the solution fits the limits and allowed
commands, and that it wins.
