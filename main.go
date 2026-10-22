// Bytewise - an isometric programming puzzle game.
//
// Program the orange robot with command tiles to power up every beacon.
// Run with: ./bytewise   (build with: go build -o bytewise .)
package main

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"log"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/text/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"

	"bytewise/engine"
)

//go:embed levels.json
var levelsData []byte

//go:embed assets/robot
var embeddedAssets embed.FS

const (
	winW, winH = 1200, 760 // logical size; the window is `scale` times bigger
	panelX     = 735
	tw, th, bh = 72.0, 36.0, 18.0 // isometric tile width, tile height, block height
	slot       = 54
	gap        = 8
	cols       = 6
	baseStep   = 0.45 // seconds per instruction at 1x speed
	dt         = 1.0 / 60
)

var board = image.Rect(0, 90, 720, 690)

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

var (
	bgTop      = rgb(22, 28, 52)
	bgBottom   = rgb(54, 44, 86)
	panelBG    = rgb(30, 34, 58)
	textCol    = rgb(236, 238, 248)
	muted      = rgb(150, 156, 186)
	accent     = rgb(255, 150, 50)
	tileTop    = rgb(176, 190, 214)
	tileLeft   = rgb(118, 130, 158)
	tileRight  = rgb(92, 102, 132)
	padOff     = rgb(52, 84, 168)
	padOffEdge = rgb(120, 160, 255)
	padOn      = rgb(255, 220, 92)
	padOnEdge  = rgb(255, 250, 210)
	white      = rgb(255, 255, 255)
	buttonCol  = rgb(70, 78, 120)
	runGreen   = rgb(46, 160, 90)
	stopRed    = rgb(200, 90, 60)
)

var cmdColors = map[string]color.RGBA{
	engine.Forward: rgb(66, 133, 244), engine.Left: rgb(38, 166, 154), engine.Right: rgb(38, 166, 154),
	engine.Jump: rgb(156, 89, 210), engine.Activate: rgb(240, 150, 30),
	engine.P1: rgb(76, 175, 80), engine.P2: rgb(226, 84, 140),
}

var progLabels = map[string]string{"main": "MAIN", "p1": "P1", "p2": "P2"}

// ---------- small helpers ----------

func lerp(a, b, t float64) float64 { return a + (b-a)*t }
func ease(t float64) float64       { return t * t * (3 - 2*t) }

func shade(c color.RGBA, k float64) color.RGBA {
	f := func(v uint8) uint8 { return uint8(math.Max(0, math.Min(255, float64(v)*k))) }
	return color.RGBA{f(c.R), f(c.G), f(c.B), 255}
}

func rect(x, y, w, h int) image.Rectangle { return image.Rect(x, y, x+w, y+h) }
func inRect(r image.Rectangle, x, y int) bool {
	return x >= r.Min.X && x < r.Max.X && y >= r.Min.Y && y < r.Max.Y
}
func centerX(r image.Rectangle) float64 { return float64(r.Min.X+r.Max.X) / 2 }
func centerY(r image.Rectangle) float64 { return float64(r.Min.Y+r.Max.Y) / 2 }

type pt struct{ x, y float64 }

func polyPath(pts []pt, closed bool) *vector.Path {
	var p vector.Path
	for i, q := range pts {
		if i == 0 {
			p.MoveTo(float32(q.x), float32(q.y))
		} else {
			p.LineTo(float32(q.x), float32(q.y))
		}
	}
	if closed {
		p.Close()
	}
	return &p
}

func roundRectPath(r image.Rectangle, radius float32) *vector.Path {
	x, y := float32(r.Min.X), float32(r.Min.Y)
	w, h := float32(r.Dx()), float32(r.Dy())
	if m := min(w, h) / 2; radius > m {
		radius = m
	}
	var p vector.Path
	p.MoveTo(x+radius, y)
	p.LineTo(x+w-radius, y)
	p.ArcTo(x+w, y, x+w, y+radius, radius)
	p.LineTo(x+w, y+h-radius)
	p.ArcTo(x+w, y+h, x+w-radius, y+h, radius)
	p.LineTo(x+radius, y+h)
	p.ArcTo(x, y+h, x, y+h-radius, radius)
	p.LineTo(x, y+radius)
	p.ArcTo(x, y, x+radius, y, radius)
	p.Close()
	return &p
}

func ellipsePath(cx, cy, rx, ry float64) *vector.Path {
	const n = 40
	pts := make([]pt, n)
	for i := range pts {
		a := float64(i) / n * 2 * math.Pi
		pts[i] = pt{cx + math.Cos(a)*rx, cy + math.Sin(a)*ry}
	}
	return polyPath(pts, true)
}

func fillPath(dst *ebiten.Image, p *vector.Path, clr color.Color) {
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(clr)
	vector.FillPath(dst, p, nil, op)
}

func strokePath(dst *ebiten.Image, p *vector.Path, width float32, clr color.Color) {
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(clr)
	vector.StrokePath(dst, p, &vector.StrokeOptions{Width: width, LineJoin: vector.LineJoinRound}, op)
}

func fillPoly(dst *ebiten.Image, pts []pt, clr color.Color) { fillPath(dst, polyPath(pts, true), clr) }
func strokePoly(dst *ebiten.Image, pts []pt, width float32, clr color.Color, closed bool) {
	strokePath(dst, polyPath(pts, closed), width, clr)
}
func line(dst *ebiten.Image, a, b pt, width float32, clr color.Color) {
	vector.StrokeLine(dst, float32(a.x), float32(a.y), float32(b.x), float32(b.y), width, clr, true)
}
func fillRoundRect(dst *ebiten.Image, r image.Rectangle, radius float32, clr color.Color) {
	fillPath(dst, roundRectPath(r, radius), clr)
}
func strokeRoundRect(dst *ebiten.Image, r image.Rectangle, radius, width float32, clr color.Color) {
	strokePath(dst, roundRectPath(r, radius), width, clr)
}

// ---------- fonts ----------

type fonts struct {
	normal, small, big, huge, icon, iconSmall text.Face
}

func loadFonts() (*fonts, error) {
	bold, err := text.NewGoTextFaceSource(bytes.NewReader(gobold.TTF))
	if err != nil {
		return nil, err
	}
	regular, err := text.NewGoTextFaceSource(bytes.NewReader(goregular.TTF))
	if err != nil {
		return nil, err
	}
	return &fonts{
		normal:    &text.GoTextFace{Source: regular, Size: 18},
		small:     &text.GoTextFace{Source: regular, Size: 15},
		big:       &text.GoTextFace{Source: bold, Size: 30},
		huge:      &text.GoTextFace{Source: bold, Size: 44},
		icon:      &text.GoTextFace{Source: bold, Size: 23},
		iconSmall: &text.GoTextFace{Source: bold, Size: 18},
	}, nil
}

func drawText(dst *ebiten.Image, s string, face text.Face, x, y float64, clr color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(x, y)
	op.ColorScale.ScaleWithColor(clr)
	text.Draw(dst, s, face, op)
}

func drawTextCenter(dst *ebiten.Image, s string, face text.Face, cx, cy float64, clr color.Color) {
	op := &text.DrawOptions{}
	op.GeoM.Translate(cx, cy)
	op.ColorScale.ScaleWithColor(clr)
	op.PrimaryAlign = text.AlignCenter
	op.SecondaryAlign = text.AlignCenter
	text.Draw(dst, s, face, op)
}

func textWidth(s string, face text.Face) float64 {
	w, _ := text.Measure(s, face, 0)
	return w
}

func wrap(s string, face text.Face, width float64) []string {
	var lines []string
	cur := ""
	for _, word := range strings.Fields(s) {
		trial := strings.TrimSpace(cur + " " + word)
		if textWidth(trial, face) > width && cur != "" {
			lines = append(lines, cur)
			cur = word
		} else {
			cur = trial
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// ---------- sprites ----------

type spriteMeta struct {
	Anchor []float64 `json:"anchor"`
	Scale  float64   `json:"scale"`
}

// assetDirs lists the places a user-supplied assets/robot directory may live:
// next to the executable, then the current working directory.
func assetDirs() []string {
	var dirs []string
	if exe, err := os.Executable(); err == nil {
		dirs = append(dirs, filepath.Join(filepath.Dir(exe), "assets", "robot"))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, "assets", "robot"))
	}
	return dirs
}

// loadRobotSprites returns one image per facing direction plus the anchor
// (the pixel that sits on the tile centre) and draw scale. PNGs found on disk
// override the embedded defaults; see README.md.
func loadRobotSprites() (sprites [4]*ebiten.Image, anchor pt, scale float64, err error) {
	var dir fs.FS = mustSub(embeddedAssets, "assets/robot")
	for _, d := range assetDirs() {
		ok := true
		for _, n := range engine.DirNames {
			if _, statErr := os.Stat(filepath.Join(d, "robot_"+n+".png")); statErr != nil {
				ok = false
				break
			}
		}
		if ok {
			dir = os.DirFS(d)
			break
		}
	}
	for i, n := range engine.DirNames {
		data, readErr := fs.ReadFile(dir, "robot_"+n+".png")
		if readErr != nil {
			return sprites, anchor, 1, readErr
		}
		img, decErr := png.Decode(bytes.NewReader(data))
		if decErr != nil {
			return sprites, anchor, 1, fmt.Errorf("robot_%s.png: %w", n, decErr)
		}
		sprites[i] = ebiten.NewImageFromImage(img)
	}
	scale = 1
	w, h := sprites[0].Bounds().Dx(), sprites[0].Bounds().Dy()
	anchor = pt{float64(w) / 2, float64(h) * 0.87}
	if data, readErr := fs.ReadFile(dir, "sprite.json"); readErr == nil {
		var meta spriteMeta
		if json.Unmarshal(data, &meta) == nil {
			if meta.Scale > 0 {
				scale = meta.Scale
			}
			if len(meta.Anchor) == 2 {
				anchor = pt{meta.Anchor[0] * scale, meta.Anchor[1] * scale}
			} else {
				anchor = pt{anchor.x * scale, anchor.y * scale}
			}
		}
	}
	return sprites, anchor, scale, nil
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		panic(err)
	}
	return sub
}

// ---------- command tiles ----------

// drawIcon draws the white glyph for a command inside r.
func (g *Game) drawIcon(dst *ebiten.Image, cmd string, r image.Rectangle, small bool) {
	c := white
	cx, cy := centerX(r), centerY(r)
	s := float64(r.Dx()) / 54
	w := float32(math.Max(2, math.Floor(4*s)))
	switch cmd {
	case engine.Forward:
		line(dst, pt{cx, cy + 14*s}, pt{cx, cy - 6*s}, w+1, c)
		fillPoly(dst, []pt{{cx, cy - 16*s}, {cx - 10*s, cy - 4*s}, {cx + 10*s, cy - 4*s}}, c)
	case engine.Left, engine.Right:
		k := 1.0
		if cmd == engine.Left {
			k = -1
		}
		x0 := cx - 6*s*k
		strokePoly(dst, []pt{{x0, cy + 15*s}, {x0, cy - 4*s}, {cx + 4*s*k, cy - 4*s}}, w+1, c, false)
		tip := cx + 14*s*k
		fillPoly(dst, []pt{{tip, cy - 4*s}, {tip - 11*s*k, cy - 14*s}, {tip - 11*s*k, cy + 6*s}}, c)
	case engine.Jump:
		arc := make([]pt, 9)
		for i := range arc {
			arc[i] = pt{cx - 14*s + float64(i)*2.4*s, cy + 12*s - math.Sin(float64(i)/10*math.Pi)*22*s}
		}
		strokePoly(dst, arc, w, c, false)
		fillPoly(dst, []pt{{cx + 14*s, cy + 14*s}, {cx + 3*s, cy + 5*s}, {cx + 15*s, cy + 1*s}}, c)
		line(dst, pt{cx - 18*s, cy + 17*s}, pt{cx + 18*s, cy + 17*s}, float32(math.Max(1, float64(w-1))), c)
	case engine.Activate:
		vector.FillCircle(dst, float32(cx), float32(cy-3*s), float32(9*s), c, true)
		fillRoundRect(dst, image.Rect(int(cx-5*s), int(cy+6*s), int(cx+5*s), int(cy+13*s)), float32(2*s), c)
		for a := -2; a <= 2; a++ {
			ang := (-90 + float64(a)*40) * math.Pi / 180
			p0 := pt{cx + math.Cos(ang)*13*s, cy - 3*s + math.Sin(ang)*13*s}
			p1 := pt{cx + math.Cos(ang)*18*s, cy - 3*s + math.Sin(ang)*18*s}
			line(dst, p0, p1, float32(math.Max(1, float64(w-1))), c)
		}
	default:
		face := g.fonts.icon
		if small {
			face = g.fonts.iconSmall
		}
		drawTextCenter(dst, cmd, face, cx, cy, c)
	}
}

// tilePad is the margin around a cached tile image so its drop shadow fits.
const tilePad = 4

// tileImage returns a cached rendering of a slot-sized command tile (or the
// empty slot when cmd is ""). Antialiased vector drawing is costly per shape,
// so every tile is rasterised once and then blitted.
func (g *Game) tileImage(cmd string, dim bool) *ebiten.Image {
	key := cmd
	if dim {
		key += ":dim"
	}
	if img, ok := g.tileCache[key]; ok {
		return img
	}
	img := ebiten.NewImage(slot+2*tilePad, slot+2*tilePad)
	r := rect(tilePad, tilePad, slot, slot)
	if cmd == "" {
		fillRoundRect(img, r, 10, rgb(36, 40, 68))
		strokeRoundRect(img, r, 10, 1, rgb(60, 66, 104))
	} else {
		col := cmdColors[cmd]
		if dim {
			col = shade(col, 0.45)
		}
		fillRoundRect(img, r.Add(image.Pt(0, 3)), 10, shade(col, 0.6))
		fillRoundRect(img, r, 10, col)
		strokeRoundRect(img, r, 10, 2, shade(col, 1.3))
		g.drawIcon(img, cmd, r, false)
	}
	g.tileCache[key] = img
	return img
}

func (g *Game) drawTileButton(dst *ebiten.Image, cmd string, r image.Rectangle, highlight, dim bool) {
	op := &ebiten.DrawImageOptions{}
	op.GeoM.Translate(float64(r.Min.X-tilePad), float64(r.Min.Y-tilePad))
	dst.DrawImage(g.tileImage(cmd, dim), op)
	if highlight {
		strokeRoundRect(dst, r.Inset(-3), 12, 3, rgb(255, 240, 120))
	}
}

type button struct {
	label  string
	rect   image.Rectangle
	action func()
	color  color.RGBA
}

func (b *button) draw(g *Game, dst *ebiten.Image, mx, my int) {
	col := b.color
	if inRect(b.rect, mx, my) {
		col = shade(col, 1.2)
	}
	fillRoundRect(dst, b.rect.Add(image.Pt(0, 3)), 10, shade(col, 0.6))
	fillRoundRect(dst, b.rect, 10, col)
	drawTextCenter(dst, b.label, g.fonts.normal, centerX(b.rect), centerY(b.rect), textCol)
}

// ---------- persistence ----------

type saveData struct {
	Current   int                            `json:"current"`
	Completed []int                          `json:"completed"`
	Programs  map[string]map[string][]string `json:"programs"`
	MusicOff  bool                           `json:"music_off,omitempty"`
	SfxOff    bool                           `json:"sfx_off,omitempty"`
}

func savePath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = "."
	}
	return filepath.Join(dir, "bytewise", "save.json")
}

// ---------- game ----------

type snap struct {
	x, y, h float64
	d       int
}

type anim struct {
	kind     string
	t, dur   float64
	from, to snap
}

type dragState struct {
	cmd            string
	srcProg        string
	srcIdx         int
	fromProgram    bool
	startX, startY int
	moved          bool
}

type progBox struct {
	box   image.Rectangle
	slots []image.Rectangle
}

type palItem struct {
	cmd  string
	rect image.Rectangle
}

type highlightRef struct {
	prog string
	idx  int
}

type Game struct {
	fonts      *fonts
	sprites    [4]*ebiten.Image
	anchor     pt
	spriteScl  float64
	background *ebiten.Image
	levels     []*engine.Level
	save       saveData
	savePath   string

	levelIndex int
	level      *engine.Level
	tileOrder  []engine.Pos
	programs   map[string][]string
	selected   string
	speed      int
	time       float64
	showLevels bool
	drag       *dragState
	debug      bool // BYTEWISE_DEBUG=1: show and log frame and tick rate
	ticks      int

	world     *engine.World
	interp    *engine.Interpreter
	running   bool
	anim      *anim
	highlight *highlightRef
	result    string // "" | "win" | "fail" | "overflow"

	boardBehind *ebiten.Image // cached tiles drawn before the robot
	boardFront  *ebiten.Image // cached tiles drawn after the robot
	boardSig    string        // state the cache was built for
	tileCache   map[string]*ebiten.Image

	sound        *sound
	soundStarted bool // music is started from the first Update, once the game loop runs
	quietEdits   bool // suppress place/remove sounds while a drag rearranges tiles

	paletteRects []palItem
	boxOrder     []string
	boxes        map[string]progBox
	buttons      []*button
	navButtons   []*button
	ox, oy       float64
}

// newGame loads everything. withSound=false skips the audio device entirely
// (used by tests and the -nosound flag).
func newGame(withSound bool) (*Game, error) {
	g := &Game{speed: 1, savePath: savePath(), debug: os.Getenv("BYTEWISE_DEBUG") != "",
		tileCache: map[string]*ebiten.Image{}, sound: newSound(withSound)}
	var err error
	if g.fonts, err = loadFonts(); err != nil {
		return nil, err
	}
	if g.sprites, g.anchor, g.spriteScl, err = loadRobotSprites(); err != nil {
		return nil, fmt.Errorf("loading robot sprites: %w", err)
	}
	if g.levels, err = engine.ParseLevels(levelsData); err != nil {
		return nil, err
	}
	if len(g.levels) == 0 {
		return nil, errors.New("levels.json contains no levels")
	}
	g.background = g.makeBackground()
	g.boardBehind = ebiten.NewImage(winW, winH)
	g.boardFront = ebiten.NewImage(winW, winH)
	g.loadSave()
	g.sound.musicOn = !g.save.MusicOff
	g.sound.sfxOn = !g.save.SfxOff
	g.loadLevel(min(g.save.Current, len(g.levels)-1))
	return g, nil
}

func (g *Game) loadSave() {
	g.save = saveData{Programs: map[string]map[string][]string{}}
	data, err := os.ReadFile(g.savePath)
	if err != nil {
		return
	}
	var s saveData
	if json.Unmarshal(data, &s) != nil {
		return
	}
	if s.Programs == nil {
		s.Programs = map[string]map[string][]string{}
	}
	if s.Current < 0 {
		s.Current = 0
	}
	g.save = s
}

func (g *Game) writeSave() {
	g.save.Current = g.levelIndex
	progs := map[string][]string{}
	for _, p := range engine.Programs {
		progs[p] = append([]string{}, g.programs[p]...)
	}
	g.save.Programs[strconv.Itoa(g.levelIndex)] = progs
	data, err := json.MarshalIndent(g.save, "", " ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(g.savePath), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(g.savePath, data, 0o644)
}

func (g *Game) completed(i int) bool {
	for _, c := range g.save.Completed {
		if c == i {
			return true
		}
	}
	return false
}

// ---------- level / run state ----------

func (g *Game) loadLevel(index int) {
	g.levelIndex = index
	g.level = g.levels[index]
	saved := g.save.Programs[strconv.Itoa(index)]
	g.programs = map[string][]string{}
	for _, p := range engine.Programs {
		g.programs[p] = append([]string{}, saved[p]...)
	}
	g.selected = "main"
	g.tileOrder = g.tileOrder[:0]
	for p := range g.level.Heights {
		g.tileOrder = append(g.tileOrder, p)
	}
	sort.Slice(g.tileOrder, func(i, j int) bool {
		a, b := g.tileOrder[i], g.tileOrder[j]
		if a.X+a.Y != b.X+b.Y {
			return a.X+a.Y < b.X+b.Y
		}
		return a.X < b.X
	})
	g.resetRun()
	g.layout()
	g.computeBoardOrigin()
}

func (g *Game) resetRun() {
	g.world = engine.NewWorld(g.level)
	g.interp = nil
	g.running = false
	g.anim = nil
	g.highlight = nil
	g.result = ""
}

func (g *Game) startRun() {
	g.resetRun()
	g.interp = engine.NewInterpreter(g.programs)
	g.running = true
}

func (g *Game) toggleRun() {
	if g.running || g.result != "" {
		g.resetRun()
	} else {
		g.startRun()
	}
}

func (g *Game) visiblePrograms() []string {
	var out []string
	for _, p := range engine.Programs {
		if g.level.Limits[p] > 0 {
			out = append(out, p)
		}
	}
	return out
}

func (g *Game) palette() []string {
	var out []string
	for _, c := range g.level.Commands {
		if callee, isCall := engine.Calls[c]; isCall && g.level.Limits[callee] == 0 {
			continue
		}
		out = append(out, c)
	}
	return out
}

func (g *Game) inPalette(cmd string) bool {
	for _, c := range g.palette() {
		if c == cmd {
			return true
		}
	}
	return false
}

// ---------- layout ----------

func (g *Game) layout() {
	x := panelX
	g.paletteRects = g.paletteRects[:0]
	for i, cmd := range g.palette() {
		g.paletteRects = append(g.paletteRects, palItem{cmd, rect(x+12+i*(slot+gap), 52, slot, slot)})
	}
	g.boxOrder = g.visiblePrograms()
	g.boxes = map[string]progBox{}
	y := 140
	for _, prog := range g.boxOrder {
		limit := g.level.Limits[prog]
		rows := (limit + cols - 1) / cols
		box := rect(x, y, cols*(slot+gap)+16, 34+rows*(slot+gap)+6)
		slots := make([]image.Rectangle, limit)
		for i := range slots {
			slots[i] = rect(x+12+(i%cols)*(slot+gap), y+34+(i/cols)*(slot+gap), slot, slot)
		}
		g.boxes[prog] = progBox{box, slots}
		y = box.Max.Y + 14
	}
	by := winH - 70
	g.buttons = []*button{
		{"Run", rect(x, by, 120, 46), g.toggleRun, runGreen},
		{"Clear", rect(x+132, by, 100, 46), g.clearSelected, rgb(170, 70, 70)},
		{fmt.Sprintf("Speed %dx", g.speed), rect(x+244, by, 120, 46), g.cycleSpeed, buttonCol},
	}
	g.navButtons = []*button{
		{"< Prev", rect(20, winH-62, 100, 40), func() { g.changeLevel(-1) }, buttonCol},
		{"Levels", rect(130, winH-62, 100, 40), g.openLevels, buttonCol},
		{"Next >", rect(240, winH-62, 100, 40), func() { g.changeLevel(1) }, buttonCol},
		{"", rect(372, winH-62, 104, 40), g.toggleMusic, buttonCol},
		{"", rect(486, winH-62, 104, 40), g.toggleSfx, buttonCol},
	}
	g.updateSoundLabels()
}

func onOff(on bool) string {
	if on {
		return "On"
	}
	return "Off"
}

func (g *Game) updateSoundLabels() {
	g.navButtons[3].label = "Music " + onOff(g.sound.musicOn)
	g.navButtons[4].label = "Sound " + onOff(g.sound.sfxOn)
}

func (g *Game) toggleMusic() {
	g.sound.setMusic(!g.sound.musicOn)
	g.save.MusicOff = !g.sound.musicOn
	g.updateSoundLabels()
	g.writeSave()
}

func (g *Game) toggleSfx() {
	g.sound.sfxOn = !g.sound.sfxOn
	g.save.SfxOff = !g.sound.sfxOn
	g.updateSoundLabels()
	g.writeSave()
	g.sound.play("click")
}

func (g *Game) computeBoardOrigin() {
	minX, maxX := math.Inf(1), math.Inf(-1)
	minY, maxY := math.Inf(1), math.Inf(-1)
	for p, h := range g.level.Heights {
		sx := float64(p.X-p.Y) * tw / 2
		sy := float64(p.X+p.Y) * th / 2
		minX = math.Min(minX, sx-tw/2)
		maxX = math.Max(maxX, sx+tw/2)
		minY = math.Min(minY, sy-float64(h)*bh-th/2-70)
		maxY = math.Max(maxY, sy+th/2)
	}
	g.ox = centerX(board) - (minX+maxX)/2
	g.oy = centerY(board) - (minY+maxY)/2
}

func (g *Game) iso(x, y, h float64) pt {
	return pt{g.ox + (x-y)*tw/2, g.oy + (x+y)*th/2 - h*bh}
}

// ---------- actions ----------

func (g *Game) clearSelected() {
	if len(g.programs[g.selected]) > 0 {
		g.sound.play("remove")
	}
	g.programs[g.selected] = nil
	g.onEdit()
}

func (g *Game) cycleSpeed() {
	g.speed = map[int]int{1: 2, 2: 4, 4: 1}[g.speed]
	g.buttons[2].label = fmt.Sprintf("Speed %dx", g.speed)
}

func (g *Game) changeLevel(delta int) {
	g.writeSave()
	n := len(g.levels)
	g.loadLevel(((g.levelIndex+delta)%n + n) % n)
}

func (g *Game) openLevels() { g.showLevels = true }

func (g *Game) onEdit() {
	if g.running || g.result != "" {
		g.resetRun()
	}
	g.writeSave()
}

func (g *Game) insert(prog string, index int, cmd string) bool {
	cur := g.programs[prog]
	if len(cur) >= g.level.Limits[prog] {
		return false
	}
	index = min(index, len(cur))
	cur = append(cur, "")
	copy(cur[index+1:], cur[index:])
	cur[index] = cmd
	g.programs[prog] = cur
	if !g.quietEdits {
		g.sound.play("place")
	}
	return true
}

func (g *Game) remove(prog string, index int) {
	cur := g.programs[prog]
	g.programs[prog] = append(cur[:index:index], cur[index+1:]...)
	if !g.quietEdits {
		g.sound.play("remove")
	}
}

// ---------- input ----------

// hitProgram returns the program box under (x, y) and the slot index within
// it, or -1 when the point is in the box but not on a slot.
func (g *Game) hitProgram(x, y int) (prog string, idx int, ok bool) {
	for _, p := range g.boxOrder {
		b := g.boxes[p]
		if !inRect(b.box, x, y) {
			continue
		}
		for i, r := range b.slots {
			if inRect(r, x, y) {
				return p, i, true
			}
		}
		return p, -1, true
	}
	return "", -1, false
}

func (g *Game) onMouseDown(x, y int) {
	if g.showLevels {
		for i, r := range g.levelGridRects() {
			if inRect(r, x, y) {
				g.showLevels = false
				g.sound.play("click")
				g.writeSave()
				g.loadLevel(i)
				return
			}
		}
		g.showLevels = false
		return
	}
	if g.result == "win" && inRect(g.overlayNextRect(), x, y) {
		g.sound.play("click")
		g.changeLevel(1)
		return
	}
	for _, b := range append(append([]*button{}, g.buttons...), g.navButtons...) {
		if inRect(b.rect, x, y) {
			if b.action != nil {
				g.sound.play("click")
			}
			b.action()
			return
		}
	}
	for _, p := range g.paletteRects {
		if inRect(p.rect, x, y) {
			g.drag = &dragState{cmd: p.cmd, startX: x, startY: y}
			return
		}
	}
	if prog, i, ok := g.hitProgram(x, y); ok {
		g.selected = prog
		if i >= 0 && i < len(g.programs[prog]) {
			g.drag = &dragState{cmd: g.programs[prog][i], srcProg: prog, srcIdx: i, fromProgram: true, startX: x, startY: y}
		}
	}
}

func (g *Game) onMouseMove(x, y int) {
	if g.drag != nil && !g.drag.moved {
		if abs(x-g.drag.startX)+abs(y-g.drag.startY) > 6 {
			g.drag.moved = true
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func (g *Game) onMouseUp(x, y int) {
	d := g.drag
	g.drag = nil
	if d == nil {
		return
	}
	if !d.moved {
		// click: palette appends to the selected program, a placed tile is removed
		if d.fromProgram {
			g.remove(d.srcProg, d.srcIdx)
		} else {
			g.insert(g.selected, 99, d.cmd)
		}
		g.onEdit()
		return
	}
	prog, i, hit := g.hitProgram(x, y)
	g.quietEdits = true // one sound for the whole rearrangement, chosen below
	if d.fromProgram {
		g.remove(d.srcProg, d.srcIdx)
	}
	placed, bounced := false, false
	if hit {
		index := i
		if i < 0 {
			index = len(g.programs[prog])
		}
		placed = g.insert(prog, index, d.cmd)
		if !placed && d.fromProgram {
			g.insert(d.srcProg, d.srcIdx, d.cmd) // target full: put it back
			bounced = true
		} else if placed {
			g.selected = prog
		}
	}
	g.quietEdits = false
	switch {
	case placed:
		g.sound.play("place")
	case bounced:
		g.sound.play("blocked")
	case d.fromProgram:
		g.sound.play("remove")
	}
	g.onEdit()
}

var keyCommands = map[ebiten.Key]string{
	ebiten.KeyArrowUp: engine.Forward, ebiten.KeyArrowLeft: engine.Left, ebiten.KeyArrowRight: engine.Right,
	ebiten.KeyJ: engine.Jump, ebiten.KeyA: engine.Activate, ebiten.KeyDigit1: engine.P1, ebiten.KeyDigit2: engine.P2,
}

func (g *Game) onKey(key ebiten.Key) {
	if g.showLevels {
		if key == ebiten.KeyEscape {
			g.showLevels = false
		}
		return
	}
	switch key {
	case ebiten.KeySpace:
		g.toggleRun()
	case ebiten.KeyBackspace:
		if n := len(g.programs[g.selected]); n > 0 {
			g.remove(g.selected, n-1)
			g.onEdit()
		}
	case ebiten.KeyR, ebiten.KeyEscape:
		g.resetRun()
	case ebiten.KeyTab:
		progs := g.visiblePrograms()
		for i, p := range progs {
			if p == g.selected {
				g.selected = progs[(i+1)%len(progs)]
				break
			}
		}
	case ebiten.KeyEnter, ebiten.KeyNumpadEnter:
		if g.result == "win" {
			g.changeLevel(1)
		}
	case ebiten.KeyM:
		g.toggleMusic()
	case ebiten.KeyS:
		g.toggleSfx()
	default:
		if cmd, ok := keyCommands[key]; ok && g.inPalette(cmd) {
			g.insert(g.selected, 99, cmd)
			g.onEdit()
		}
	}
}

func (g *Game) handleInput() {
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.onMouseDown(mx, my)
	}
	g.onMouseMove(mx, my)
	if inpututil.IsMouseButtonJustReleased(ebiten.MouseButtonLeft) {
		g.onMouseUp(mx, my)
	}
	for _, k := range inpututil.AppendJustPressedKeys(nil) {
		g.onKey(k)
	}
}

// ---------- simulation ----------

func (g *Game) Update() error {
	if !g.soundStarted {
		g.soundStarted = true
		g.sound.setMusic(g.sound.musicOn)
	}
	g.handleInput()
	g.time += dt
	if g.debug {
		g.ticks++
		if g.ticks%60 == 0 {
			log.Printf("%.0f fps  %.0f tps", ebiten.ActualFPS(), ebiten.ActualTPS())
		}
	}
	if !g.running {
		return nil
	}
	if g.anim != nil {
		g.anim.t += dt * float64(g.speed) / g.anim.dur
		if g.anim.t < 1 {
			return nil
		}
		g.anim = nil
		if g.world.Won() {
			g.finish("win")
			return nil
		}
	}
	instr, ok := g.interp.Next()
	if !ok {
		if g.interp.Overflow {
			g.finish("overflow")
		} else {
			g.finish("fail")
		}
		return nil
	}
	g.highlight = &highlightRef{instr.Prog, instr.Index}
	w := g.world
	before := snap{float64(w.X), float64(w.Y), float64(w.H()), w.D}
	if _, isCall := engine.Calls[instr.Cmd]; isCall {
		g.anim = &anim{kind: "call", dur: baseStep * 0.3, from: before, to: before}
		return nil
	}
	kind := w.Step(instr.Cmd)
	g.anim = &anim{kind: kind, dur: baseStep, from: before, to: snap{float64(w.X), float64(w.Y), float64(w.H()), w.D}}
	if kind == "activate" && !w.Lit[w.Pos()] {
		g.sound.play("deactivate")
	} else {
		g.sound.play(kind)
	}
	return nil
}

func (g *Game) finish(result string) {
	g.running = false
	g.result = result
	g.highlight = nil
	if result == "win" {
		g.sound.play("win")
	} else {
		g.sound.play("fail")
	}
	if result == "win" && !g.completed(g.levelIndex) {
		g.save.Completed = append(g.save.Completed, g.levelIndex)
		sort.Ints(g.save.Completed)
	}
	if result == "win" {
		g.writeSave()
	}
}

type visual struct {
	x, y, h     float64
	d           int
	lift, shake float64
}

// robotVisual returns the interpolated robot pose for drawing.
func (g *Game) robotVisual() visual {
	w := g.world
	a := g.anim
	if a == nil {
		lift := 0.0
		if g.result == "win" {
			lift = math.Abs(math.Sin(g.time*6)) * 10
		}
		return visual{float64(w.X), float64(w.Y), float64(w.H()), w.D, lift, 0}
	}
	t := math.Min(1, a.t)
	f, to := a.from, a.to
	e := ease(t)
	switch a.kind {
	case "move":
		return visual{lerp(f.x, to.x, e), lerp(f.y, to.y, e), f.h, f.d, 0, 0}
	case "jump":
		arc := math.Sin(t*math.Pi) * (bh*1.6 + math.Max(0, to.h-f.h)*bh*0.5)
		return visual{lerp(f.x, to.x, t), lerp(f.y, to.y, t), lerp(f.h, to.h, t), f.d, arc, 0}
	case "turn":
		d := f.d
		if t >= 0.5 {
			d = to.d
		}
		return visual{f.x, f.y, f.h, d, math.Sin(t*math.Pi) * 4, 0}
	case "activate":
		return visual{f.x, f.y, f.h, f.d, math.Sin(t*math.Pi) * 8, 0}
	case "blocked":
		return visual{f.x, f.y, f.h, f.d, 0, math.Sin(t*math.Pi*6) * 4 * (1 - t)}
	}
	return visual{f.x, f.y, f.h, f.d, 0, 0}
}

// ---------- drawing ----------

func (g *Game) makeBackground() *ebiten.Image {
	img := image.NewRGBA(image.Rect(0, 0, winW, winH))
	for y := 0; y < winH; y++ {
		t := float64(y) / winH
		c := color.RGBA{
			uint8(lerp(float64(bgTop.R), float64(bgBottom.R), t)),
			uint8(lerp(float64(bgTop.G), float64(bgBottom.G), t)),
			uint8(lerp(float64(bgTop.B), float64(bgBottom.B), t)), 255}
		for x := 0; x < winW; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	bg := ebiten.NewImageFromImage(img)
	vector.FillRect(bg, panelX-12, 0, winW-panelX+12, winH, panelBG, false)
	vector.StrokeLine(bg, panelX-12, 0, panelX-12, winH, 2, rgb(70, 76, 120), false)
	return bg
}

func (g *Game) drawTile(dst *ebiten.Image, p engine.Pos, h int) {
	c := g.iso(float64(p.X), float64(p.Y), float64(h))
	sx, sy := c.x, c.y
	hw, hh := tw/2, th/2
	depth := float64(h) * bh
	left := []pt{{sx - hw, sy}, {sx, sy + hh}, {sx, sy + hh + depth}, {sx - hw, sy + depth}}
	right := []pt{{sx + hw, sy}, {sx, sy + hh}, {sx, sy + hh + depth}, {sx + hw, sy + depth}}
	top := []pt{{sx, sy - hh}, {sx + hw, sy}, {sx, sy + hh}, {sx - hw, sy}}
	fillPoly(dst, left, tileLeft)
	fillPoly(dst, right, tileRight)
	for k := 1; k < h; k++ {
		kk := float64(k) * bh
		line(dst, pt{sx - hw, sy + kk}, pt{sx, sy + hh + kk}, 1, shade(tileLeft, 0.85))
		line(dst, pt{sx + hw, sy + kk}, pt{sx, sy + hh + kk}, 1, shade(tileRight, 0.85))
	}
	fillPoly(dst, top, tileTop)
	strokePoly(dst, top, 2, shade(tileTop, 0.75), true)
	strokePoly(dst, []pt{left[0], left[3], left[2], right[3], right[0]}, 1, shade(tileRight, 0.7), false)
	if g.level.Pads[p] {
		inner := []pt{{sx, sy - hh*0.62}, {sx + hw*0.62, sy}, {sx, sy + hh*0.62}, {sx - hw*0.62, sy}}
		if g.world.Lit[p] {
			fillPoly(dst, inner, padOn)
			strokePoly(dst, inner, 2, padOnEdge, true)
		} else {
			fillPoly(dst, inner, padOff)
			strokePoly(dst, inner, 2, padOffEdge, true)
		}
	}
}

// drawGlow draws the pulsing halo of a lit pad. It animates every frame, so
// it is drawn live rather than into the cached board layers.
func (g *Game) drawGlow(dst *ebiten.Image, p engine.Pos) {
	c := g.iso(float64(p.X), float64(p.Y), float64(g.level.Heights[p]))
	pulse := 0.75 + 0.25*math.Sin(g.time*4)
	for r := 0; r < 4; r++ {
		rx := (tw*1.5 - float64(r)*tw*0.22) / 2
		ry := (th*1.5 - float64(r)*th*0.22) / 2
		fillPath(dst, ellipsePath(c.x, c.y, rx, ry), color.NRGBA{255, 214, 90, uint8(26 * pulse)})
	}
}

// behindRobot reports whether tile p is drawn before the robot (so the robot
// can stand on it or in front of it) for the given robot sort key.
func behindRobot(p engine.Pos, keySum, keyX int) bool {
	return p.X+p.Y < keySum || (p.X+p.Y == keySum && p.X <= keyX)
}

func (g *Game) drawRobot(dst *ebiten.Image, v visual) {
	c := g.iso(v.x, v.y, v.h)
	op := &ebiten.DrawImageOptions{}
	if g.spriteScl != 1 {
		op.GeoM.Scale(g.spriteScl, g.spriteScl)
		op.Filter = ebiten.FilterLinear
	}
	op.GeoM.Translate(c.x-g.anchor.x+v.shake, c.y-g.anchor.y-v.lift)
	dst.DrawImage(g.sprites[v.d], op)
}

func (g *Game) drawBoard(dst *ebiten.Image) {
	v := g.robotVisual()
	var keySum, keyX int
	if a := g.anim; a != nil {
		s0 := int(a.from.x + a.from.y)
		s1 := int(a.to.x + a.to.y)
		if s1 > s0 || (s1 == s0 && a.to.x > a.from.x) {
			keySum, keyX = s1, int(a.to.x)
		} else {
			keySum, keyX = s0, int(a.from.x)
		}
	} else {
		keySum, keyX = g.world.X+g.world.Y, g.world.X
	}
	// The tiles are expensive vector art, so they are cached in two layers
	// (behind / in front of the robot) and only redrawn when the robot's
	// depth position or a pad's state changes.
	lit := make([]string, 0, len(g.world.Lit))
	for p := range g.world.Lit {
		lit = append(lit, fmt.Sprintf("%d,%d", p.X, p.Y))
	}
	sort.Strings(lit)
	sig := fmt.Sprintf("%d:%d:%d:%s", g.levelIndex, keySum, keyX, strings.Join(lit, ";"))
	if sig != g.boardSig {
		g.boardBehind.Clear()
		g.boardFront.Clear()
		for _, p := range g.tileOrder {
			layer := g.boardFront
			if behindRobot(p, keySum, keyX) {
				layer = g.boardBehind
			}
			g.drawTile(layer, p, g.level.Heights[p])
		}
		g.boardSig = sig
	}
	dst.DrawImage(g.boardBehind, nil)
	for p := range g.world.Lit {
		if behindRobot(p, keySum, keyX) {
			g.drawGlow(dst, p)
		}
	}
	g.drawRobot(dst, v)
	dst.DrawImage(g.boardFront, nil)
	for p := range g.world.Lit {
		if !behindRobot(p, keySum, keyX) {
			g.drawGlow(dst, p)
		}
	}
}

func (g *Game) drawHeader(dst *ebiten.Image) {
	drawText(dst, fmt.Sprintf("Level %d: %s", g.levelIndex+1, g.level.Name), g.fonts.big, 24, 18, textCol)
	y := 54.0
	for _, l := range wrap(g.level.Hint, g.fonts.normal, float64(board.Dx()-48)) {
		drawText(dst, l, g.fonts.normal, 24, y, muted)
		y += 22
	}
	lit, total := len(g.world.Lit), len(g.level.Pads)
	info := fmt.Sprintf("Beacons %d/%d", lit, total)
	col := textCol
	if lit == total {
		col = padOn
	}
	drawText(dst, info, g.fonts.normal, float64(board.Max.X)-textWidth(info, g.fonts.normal)-24, winH-52, col)
}

func (g *Game) drawPanel(dst *ebiten.Image, mx, my int) {
	drawText(dst, "COMMANDS", g.fonts.normal, panelX+12, 24, muted)
	for _, p := range g.paletteRects {
		g.drawTileButton(dst, p.cmd, p.rect, false, false)
	}
	dragging := g.drag != nil && g.drag.moved
	for _, prog := range g.boxOrder {
		b := g.boxes[prog]
		sel := prog == g.selected
		fillRoundRect(dst, b.box, 12, rgb(44, 50, 84))
		labelCol, edge, width := textCol, buttonCol, float32(2)
		if sel {
			labelCol, edge, width = accent, accent, 3
		}
		strokeRoundRect(dst, b.box, 12, width, edge)
		drawText(dst, progLabels[prog], g.fonts.normal, float64(b.box.Min.X+14), float64(b.box.Min.Y+9), labelCol)
		count := fmt.Sprintf("%d/%d", len(g.programs[prog]), len(b.slots))
		drawText(dst, count, g.fonts.small, float64(b.box.Max.X)-textWidth(count, g.fonts.small)-14, float64(b.box.Min.Y+11), muted)
		over := dragging && inRect(b.box, mx, my)
		cmds := g.programs[prog]
		for i, r := range b.slots {
			if i < len(cmds) {
				hl := g.highlight != nil && g.highlight.prog == prog && g.highlight.idx == i
				dim := dragging && g.drag.fromProgram && g.drag.srcProg == prog && g.drag.srcIdx == i
				g.drawTileButton(dst, cmds[i], r, hl, dim)
			} else {
				g.drawTileButton(dst, "", r, false, false)
			}
			if over && inRect(r, mx, my) {
				x := float64(r.Min.X) - gap/2.0
				line(dst, pt{x, float64(r.Min.Y)}, pt{x, float64(r.Max.Y)}, 3, accent)
			}
		}
	}
	tips := []string{
		"Click or drag commands in; click a placed tile to remove it.",
		"Space: run/stop   Tab: switch program   Backspace: undo",
	}
	for i, tip := range tips {
		drawText(dst, tip, g.fonts.small, panelX, float64(winH-118+i*20), muted)
	}
	run := g.buttons[0]
	switch {
	case g.running:
		run.label, run.color = "Stop", stopRed
	case g.result != "":
		run.label, run.color = "Reset", stopRed
	default:
		run.label, run.color = "Run", runGreen
	}
	for _, b := range g.buttons {
		b.draw(g, dst, mx, my)
	}
}

func (g *Game) overlayNextRect() image.Rectangle {
	return rect(int(centerX(board))-90, board.Min.Y+120, 180, 50)
}

func (g *Game) drawResult(dst *ebiten.Image, mx, my int) {
	switch g.result {
	case "win":
		drawTextCenter(dst, "All beacons lit!", g.fonts.huge, centerX(board), float64(board.Min.Y+70), padOn)
		label := "Next level"
		if g.levelIndex == len(g.levels)-1 {
			label = "Play again"
		}
		b := button{label, g.overlayNextRect(), nil, runGreen}
		b.draw(g, dst, mx, my)
	case "fail", "overflow":
		msg := "Some beacons are still dark. Press Reset and try again."
		if g.result == "overflow" {
			msg = "Too many steps - is something looping forever?"
		}
		w, h := text.Measure(msg, g.fonts.normal, 0)
		cx, cy := centerX(board), float64(board.Min.Y+40)
		box := image.Rect(int(cx-w/2)-15, int(cy-h/2)-9, int(cx+w/2)+15, int(cy+h/2)+9)
		fillRoundRect(dst, box, 10, rgb(60, 30, 40))
		drawTextCenter(dst, msg, g.fonts.normal, cx, cy, rgb(255, 150, 140))
	}
}

func (g *Game) levelGridRects() []image.Rectangle {
	const c, w, h = 4, 200, 110
	x0 := (winW-c*(w+20))/2 + 10
	out := make([]image.Rectangle, len(g.levels))
	for i := range out {
		out[i] = rect(x0+(i%c)*(w+20), 180+(i/c)*(h+20), w, h)
	}
	return out
}

func (g *Game) drawLevelSelect(dst *ebiten.Image, mx, my int) {
	vector.FillRect(dst, 0, 0, winW, winH, color.NRGBA{10, 12, 24, 220}, false)
	drawTextCenter(dst, "Choose a level", g.fonts.huge, winW/2, 110, textCol)
	for i, r := range g.levelGridRects() {
		col := rgb(60, 66, 110)
		if g.completed(i) {
			col = rgb(60, 120, 80)
		}
		if inRect(r, mx, my) {
			col = shade(col, 1.25)
		}
		fillRoundRect(dst, r, 14, col)
		if i == g.levelIndex {
			strokeRoundRect(dst, r, 14, 3, accent)
		}
		drawTextCenter(dst, strconv.Itoa(i+1), g.fonts.big, centerX(r), float64(r.Min.Y+36), textCol)
		drawTextCenter(dst, g.levels[i].Name, g.fonts.small, centerX(r), float64(r.Min.Y+72), textCol)
		if g.completed(i) {
			drawText(dst, "complete", g.fonts.small, float64(r.Min.X+10), float64(r.Max.Y-22), padOn)
		}
	}
	drawTextCenter(dst, "Click a level, or press Esc to go back", g.fonts.normal, winW/2, winH-60, muted)
}

func (g *Game) Draw(screen *ebiten.Image) {
	mx, my := ebiten.CursorPosition()
	screen.DrawImage(g.background, nil)
	g.drawBoard(screen)
	g.drawHeader(screen)
	g.drawPanel(screen, mx, my)
	for _, b := range g.navButtons {
		b.draw(g, screen, mx, my)
	}
	g.drawResult(screen, mx, my)
	if g.drag != nil && g.drag.moved {
		g.drawTileButton(screen, g.drag.cmd, rect(mx-slot/2, my-slot/2, slot, slot), false, false)
	}
	if g.showLevels {
		g.drawLevelSelect(screen, mx, my)
	}
	if g.debug {
		drawText(screen, fmt.Sprintf("%.0f fps  %.0f tps", ebiten.ActualFPS(), ebiten.ActualTPS()), g.fonts.small, 24, winH-30, muted)
	}
}

func (g *Game) Layout(outsideWidth, outsideHeight int) (int, int) { return winW, winH }

func main() {
	scale := flag.Int("scale", 0, "window scale factor (1 or 2); 0 picks the largest that fits the screen")
	noSound := flag.Bool("nosound", false, "do not open the audio device at all")
	flag.Parse()
	if *scale <= 0 {
		*scale = 2
		if mw, mh := ebiten.Monitor().Size(); mw < winW*2 || mh < winH*2 {
			*scale = 1
		}
	}
	g, err := newGame(!*noSound)
	if err != nil {
		log.Fatal(err)
	}
	ebiten.SetWindowTitle("Bytewise")
	ebiten.SetWindowSize(winW**scale, winH**scale)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	if err := ebiten.RunGame(g); err != nil {
		// Ebitengine stops the game loop when the audio device cannot be opened
		// (no sound server, no ALSA device). The game is still perfectly
		// playable silently, so relaunch this executable with sound disabled.
		if !*noSound && strings.Contains(err.Error(), "audio") {
			log.Printf("%v\nrestarting without sound", err)
			g.writeSave()
			if exe, exeErr := os.Executable(); exeErr == nil {
				_ = syscall.Exec(exe, append(os.Args, "-nosound"), os.Environ()) // only returns on failure
			}
		}
		log.Fatal(err)
	}
	g.writeSave()
}
