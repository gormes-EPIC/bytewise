// Package engine holds the game rules for Bytewise: level loading, robot
// movement and the program interpreter.
//
// It has no dependency on the renderer, so it can be unit tested headlessly.
package engine

import (
	"encoding/json"
	"fmt"
	"os"
)

// Pos is a grid coordinate.
type Pos struct{ X, Y int }

// Command tiles.
const (
	Forward  = "F"
	Left     = "L"
	Right    = "R"
	Jump     = "J"
	Activate = "A"
	P1       = "P1"
	P2       = "P2"
)

// AllCommands lists every command in palette order.
var AllCommands = []string{Forward, Left, Right, Jump, Activate, P1, P2}

// Programs lists the program slots in display order.
var Programs = []string{"main", "p1", "p2"}

// Calls maps a procedure command to the program it runs.
var Calls = map[string]string{P1: "p1", P2: "p2"}

// Dirs maps a direction index to a grid delta.
// 0:+x (screen SE), 1:+y (SW), 2:-x (NW), 3:-y (NE).
var Dirs = [4]Pos{{1, 0}, {0, 1}, {-1, 0}, {0, -1}}

// DirNames maps a direction index to its sprite suffix.
var DirNames = [4]string{"se", "sw", "nw", "ne"}

const (
	// MaxSteps is the number of instructions a run may execute before it is
	// declared to be looping forever.
	MaxSteps = 1000
	// MaxDepth is the maximum procedure call depth.
	MaxDepth = 200
)

// Level is one puzzle.
type Level struct {
	Name     string
	Hint     string
	Heights  map[Pos]int  // tile height per occupied cell
	Pads     map[Pos]bool // beacon pads
	Start    Pos
	StartDir int
	Commands []string       // palette available on this level
	Limits   map[string]int // slot count per program; 0 hides the program
}

type levelJSON struct {
	Name     string         `json:"name"`
	Hint     string         `json:"hint"`
	Heights  []string       `json:"heights"`
	Pads     [][2]int       `json:"pads"`
	Start    [3]int         `json:"start"`
	Commands []string       `json:"commands"`
	Limits   map[string]int `json:"limits"`
}

// ParseLevels decodes the levels.json format.
func ParseLevels(data []byte) ([]*Level, error) {
	var raw []levelJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("levels: %w", err)
	}
	levels := make([]*Level, 0, len(raw))
	for i, d := range raw {
		lv := &Level{
			Name:    d.Name,
			Hint:    d.Hint,
			Heights: map[Pos]int{},
			Pads:    map[Pos]bool{},
			Limits:  map[string]int{},
		}
		for y, row := range d.Heights {
			for x, ch := range row {
				if ch == '.' {
					continue
				}
				if ch < '0' || ch > '9' {
					return nil, fmt.Errorf("level %d: bad height %q", i+1, ch)
				}
				lv.Heights[Pos{x, y}] = int(ch - '0')
			}
		}
		for _, p := range d.Pads {
			lv.Pads[Pos{p[0], p[1]}] = true
		}
		lv.Start = Pos{d.Start[0], d.Start[1]}
		lv.StartDir = d.Start[2]
		if d.Commands != nil {
			lv.Commands = d.Commands
		} else {
			lv.Commands = append([]string(nil), AllCommands...)
		}
		for _, name := range Programs {
			lv.Limits[name] = d.Limits[name]
		}
		levels = append(levels, lv)
	}
	return levels, nil
}

// LoadLevels reads and parses a levels.json file.
func LoadLevels(path string) ([]*Level, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseLevels(data)
}

// Allows reports whether cmd is in this level's palette.
func (l *Level) Allows(cmd string) bool {
	for _, c := range l.Commands {
		if c == cmd {
			return true
		}
	}
	return false
}

// World is the mutable robot and beacon state for one run of a level.
type World struct {
	Level   *Level
	X, Y, D int
	Lit     map[Pos]bool
}

// NewWorld puts the robot at the level's start with every beacon dark.
func NewWorld(l *Level) *World {
	return &World{Level: l, X: l.Start.X, Y: l.Start.Y, D: l.StartDir, Lit: map[Pos]bool{}}
}

// Pos returns the robot's cell.
func (w *World) Pos() Pos { return Pos{w.X, w.Y} }

// H returns the height of the tile under the robot.
func (w *World) H() int { return w.Level.Heights[w.Pos()] }

// Ahead returns the cell in front of the robot.
func (w *World) Ahead() Pos {
	d := Dirs[w.D]
	return Pos{w.X + d.X, w.Y + d.Y}
}

// Won reports whether every beacon is lit.
func (w *World) Won() bool {
	if len(w.Lit) != len(w.Level.Pads) {
		return false
	}
	for p := range w.Lit {
		if !w.Level.Pads[p] {
			return false
		}
	}
	return true
}

// Step applies one primitive command and returns an event name for the
// animator: "turn", "activate", "move", "jump" or "blocked".
func (w *World) Step(cmd string) string {
	switch cmd {
	case Left:
		w.D = (w.D + 3) % 4
		return "turn"
	case Right:
		w.D = (w.D + 1) % 4
		return "turn"
	case Activate:
		p := w.Pos()
		if !w.Level.Pads[p] {
			return "blocked"
		}
		if w.Lit[p] {
			delete(w.Lit, p)
		} else {
			w.Lit[p] = true
		}
		return "activate"
	case Forward, Jump:
		target := w.Ahead()
		th, ok := w.Level.Heights[target]
		if !ok {
			return "blocked"
		}
		h := w.H()
		if cmd == Forward && th == h {
			w.X, w.Y = target.X, target.Y
			return "move"
		}
		if cmd == Jump && (th == h+1 || th < h) {
			w.X, w.Y = target.X, target.Y
			return "jump"
		}
		return "blocked"
	}
	panic(fmt.Sprintf("unknown command %q", cmd))
}

// Instr identifies one instruction about to execute.
type Instr struct {
	Prog  string
	Index int
	Cmd   string
}

type frame struct {
	prog string
	idx  int
}

// Interpreter steps through main/p1/p2 one instruction at a time, following
// procedure calls.
type Interpreter struct {
	Programs map[string][]string
	Steps    int
	Overflow bool
	stack    []frame
}

// NewInterpreter starts execution at the first instruction of main.
func NewInterpreter(programs map[string][]string) *Interpreter {
	return &Interpreter{Programs: programs, stack: []frame{{"main", 0}}}
}

// Next returns the next instruction, or ok=false when the program has
// finished or exceeded its step or depth budget (see Overflow).
func (it *Interpreter) Next() (instr Instr, ok bool) {
	for len(it.stack) > 0 {
		top := &it.stack[len(it.stack)-1]
		prog := it.Programs[top.prog]
		if top.idx >= len(prog) {
			it.stack = it.stack[:len(it.stack)-1]
			continue
		}
		idx := top.idx
		top.idx++
		it.Steps++
		if it.Steps > MaxSteps {
			it.Overflow = true
			return Instr{}, false
		}
		cmd := prog[idx]
		if callee, isCall := Calls[cmd]; isCall {
			if len(it.stack) >= MaxDepth {
				it.Overflow = true
				return Instr{}, false
			}
			it.stack = append(it.stack, frame{callee, 0})
		}
		return Instr{Prog: top.prog, Index: idx, Cmd: cmd}, true
	}
	return Instr{}, false
}

// Run executes programs on level headlessly and returns the final World.
func Run(level *Level, programs map[string][]string) *World {
	world := NewWorld(level)
	it := NewInterpreter(programs)
	for !world.Won() {
		instr, ok := it.Next()
		if !ok {
			break
		}
		if _, isCall := Calls[instr.Cmd]; !isCall {
			world.Step(instr.Cmd)
		}
	}
	return world
}
