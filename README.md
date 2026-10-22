# Bytewise

An isometric programming-puzzle game written in Go with [Ebitengine](https://ebitengine.org/).
Program the orange robot with command tiles so it powers up every beacon on the board.

## Run the prebuilt binary (Ubuntu 26.04, x86-64)

`bytewise` is a single static executable with the levels, fonts and robot sprites built in.
It needs only a graphical session (X11 or Wayland) and OpenGL, which every Ubuntu desktop has.

```bash
chmod +x bytewise
./bytewise
```

The window opens at 2400×1520 when the screen is big enough, otherwise at 1200×760.
Force a size with `./bytewise -scale 1` or `./bytewise -scale 2`. The window is resizable.
`./bytewise -nosound` runs silently without opening the audio device. If the device cannot be
opened (no sound server, no ALSA device), the game relaunches itself this way automatically.

## Build from source

Requirements: Go 1.26 or newer. No C compiler or system development libraries are needed.

```bash
go build -o bytewise .                                   # debug build
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bytewise .   # release build, static
go test ./...                                            # engine rules + UI logic tests
```

### Troubleshooting

- **No window appears on Linux**: you need a graphical session. Over SSH, the game can't
  open a window unless you have display forwarding.
- **To reset progress**, delete `~/.config/bytewise/save.json`.

## How to play

| Tile | Command   | What it does |
|------|-----------|--------------|
| ↑    | Forward   | Step one tile ahead (only onto a tile of the same height) |
| ↰ ↱  | Turn L/R  | Rotate 90° in place |
| ⤴    | Jump      | Hop forward up exactly one block, or down any number of blocks |
| 💡   | Activate  | Toggle the beacon under the robot |
| P1/P2| Procedure | Run the P1/P2 program, then continue (procedures may call themselves) |

- Click a command to add it to the selected program (orange outline), or drag it to a slot.
- Click a placed tile to remove it, or drag it to move it between programs.
- **Space** run/stop · **Tab** switch program · **Backspace** remove last · arrows / `J` / `A` / `1` / `2` add commands.
- **M** toggles the music and **S** the sound effects; the buttons at the bottom of the board do the same. Both settings are remembered.

## Sound

The music and every sound effect are synthesised when the game starts, so there are no audio
files. The music is a slow looping piece over C, Am, F and G; the effects are short tones
for moving, jumping, turning, lighting a beacon, bumping into something, placing and
removing tiles, and winning or failing a run. See `audio.go` to change any of them.

## Stuck on a level?

There is no loop tile. A procedure that calls itself as its last command repeats forever,
and the run stops as soon as every beacon is lit. So for a repeating pattern, put one
repetition in P1, make P1's last command P1, and have MAIN call P1 once. Level 10
introduces this; level 12 is the same idea with a longer repetition.

Progress and your programs are saved to `~/.config/bytewise/save.json`.

## Swapping the robot

The default robot is four PNGs embedded in the binary, one per facing direction. To use your
own art, create an `assets/robot/` directory next to the executable (or in the directory you
run it from) containing all four files:

| File           | Facing (on screen) |
|----------------|--------------------|
| `robot_se.png` | down-right (toward viewer) |
| `robot_sw.png` | down-left (toward viewer)  |
| `robot_nw.png` | up-left (away from viewer) |
| `robot_ne.png` | up-right (away from viewer)|

Any size works. An optional `sprite.json` in the same directory controls placement:

```json
{"anchor": [28, 66], "scale": 1.0}
```

`anchor` is the pixel in your image that should sit on the centre of the tile (the robot's
feet), and `scale` resizes the images. If `sprite.json` is missing, the anchor defaults to
the horizontal centre, 87% of the way down. `python make_sprites.py` (needs Pillow)
regenerates the default orange robot in `assets/robot/`; rebuild afterwards to embed it.

## Levels

The levels live in `levels.json`, which is embedded at build time. Each `heights` row is a
string of digits, one per tile (`.` means no tile). `start` is `[x, y, dir]`, where dir is
0=SE, 1=SW, 2=NW, 3=NE. `limits` sets the slot count for `main`/`p1`/`p2` (0 or missing
hides that program). `go test ./engine` checks that every level can be solved. Add a
reference solution to `solutions` in `engine/engine_test.go` whenever you add a level.

## Files

- `main.go`: Ebitengine rendering, input, animation and the save file
- `audio.go`: synthesised music and sound effects
- `engine/`: game rules and program interpreter (no graphics dependency)
- `levels.json`: level data
- `assets/robot/`: default robot sprites
- `make_sprites.py`: draws the default robot PNGs with Pillow (optional)
- `main.py`, `engine.py`, `test_levels.py`: the original Python/pygame implementation, kept
  for reference; it is no longer the primary version
