// Checks that every level is solvable within its slot limits.
//
// Run with: go test ./...
package engine

import (
	"fmt"
	"strings"
	"testing"
)

const levelsPath = "../levels.json"

// solutions holds one reference solution per level, in level order.
var solutions = []map[string]string{
	{"main": "F F F A"},
	{"main": "F F R F F A"},
	{"main": "F F F A R F F R F F F A"},
	{"main": "F J J A"},
	{"main": "J J A J J A"},
	{"main": "J J A R J J A"},
	{"main": "P1 P1 P1 P1", "p1": "F F F F A R"},
	{"main": "P1 P1 P1", "p1": "J R J A L"},
	{"main": "P1 P1 P1", "p1": "F R P2 R F", "p2": "F F A R R F F"},
	{"main": "P1", "p1": "F F A P1"},
	{"main": "P1 P1 P1 P1 P2", "p1": "J J J J A R", "p2": "J R J L J R J A"},
	{"main": "P1", "p1": "J R J A L P1"},
}

func parse(sol map[string]string) map[string][]string {
	progs := map[string][]string{}
	for _, name := range Programs {
		progs[name] = strings.Fields(sol[name])
	}
	return progs
}

func mustLoad(t *testing.T) []*Level {
	t.Helper()
	levels, err := LoadLevels(levelsPath)
	if err != nil {
		t.Fatal(err)
	}
	return levels
}

func TestEveryLevelHasASolution(t *testing.T) {
	if got, want := len(mustLoad(t)), len(solutions); got != want {
		t.Fatalf("%d levels but %d solutions", got, want)
	}
}

func TestSolutionsWinWithinLimits(t *testing.T) {
	levels := mustLoad(t)
	for i, level := range levels {
		if i >= len(solutions) {
			break
		}
		programs := parse(solutions[i])
		for name, prog := range programs {
			if len(prog) > level.Limits[name] {
				t.Errorf("level %d: %s too long (%d > %d)", i+1, name, len(prog), level.Limits[name])
			}
			for _, c := range prog {
				if !level.Allows(c) {
					t.Errorf("level %d: disallowed command %s", i+1, c)
				}
			}
		}
		world := Run(level, programs)
		if !world.Won() {
			t.Errorf("level %d (%s) not solved", i+1, level.Name)
			continue
		}
		var used []string
		for _, name := range Programs {
			if level.Limits[name] > 0 {
				used = append(used, fmt.Sprintf("%s %d/%d", name, len(programs[name]), level.Limits[name]))
			}
		}
		t.Logf("level %2d %-16s palette %-18s %-28s beacons %d/%d  solution %v",
			i+1, level.Name, strings.Join(level.Commands, ""), strings.Join(used, "  "),
			len(world.Lit), len(level.Pads), solutions[i])
	}
}

func TestLevelsAreWellFormed(t *testing.T) {
	for i, level := range mustLoad(t) {
		if _, ok := level.Heights[level.Start]; !ok {
			t.Errorf("level %d: start is not on a tile", i+1)
		}
		if level.StartDir < 0 || level.StartDir > 3 {
			t.Errorf("level %d: bad start direction %d", i+1, level.StartDir)
		}
		for p := range level.Pads {
			if _, ok := level.Heights[p]; !ok {
				t.Errorf("level %d: pad %v is not on a tile", i+1, p)
			}
		}
		for _, c := range level.Commands {
			known := false
			for _, k := range AllCommands {
				known = known || k == c
			}
			if !known {
				t.Errorf("level %d: unknown command %q", i+1, c)
			}
		}
	}
}

func TestEmptyProgramDoesNotWin(t *testing.T) {
	for i, level := range mustLoad(t) {
		if Run(level, parse(nil)).Won() {
			t.Errorf("level %d: empty program wins", i+1)
		}
	}
}

func TestInfiniteRecursionTerminates(t *testing.T) {
	level := mustLoad(t)[0]
	programs := parse(map[string]string{"main": "P1", "p1": "L P1"})
	it := NewInterpreter(programs)
	for {
		if _, ok := it.Next(); !ok {
			break
		}
	}
	if !it.Overflow {
		t.Error("expected overflow")
	}
	if Run(level, programs).Won() {
		t.Error("looping program should not win")
	}
}

func TestMovementRules(t *testing.T) {
	levels, err := ParseLevels([]byte(`[{"name":"t","heights":["12.","1.3"],"pads":[[0,0]],"start":[0,0,0],"limits":{"main":4}}]`))
	if err != nil {
		t.Fatal(err)
	}
	w := NewWorld(levels[0])
	if ev := w.Step(Forward); ev != "blocked" || w.X != 0 {
		t.Errorf("forward onto higher tile: got %s at x=%d", ev, w.X)
	}
	if ev := w.Step(Jump); ev != "jump" || w.X != 1 {
		t.Errorf("jump up one: got %s at x=%d", ev, w.X)
	}
	if ev := w.Step(Forward); ev != "blocked" {
		t.Errorf("forward off the board: got %s", ev)
	}
	if ev := w.Step(Right); ev != "turn" || w.D != 1 {
		t.Errorf("turn right: got %s dir=%d", ev, w.D)
	}
	if ev := w.Step(Jump); ev != "blocked" {
		t.Errorf("jump into a hole: got %s", ev)
	}
	w.Step(Right) // now facing NW (-x), back toward the start tile
	if ev := w.Step(Jump); ev != "jump" || w.X != 0 {
		t.Errorf("jump down one: got %s at x=%d", ev, w.X)
	}
	if ev := w.Step(Activate); ev != "activate" || !w.Lit[Pos{0, 0}] {
		t.Errorf("activate on pad: got %s lit=%v", ev, w.Lit)
	}
	if ev := w.Step(Activate); ev != "activate" || w.Lit[Pos{0, 0}] {
		t.Errorf("activate toggles off: got %s lit=%v", ev, w.Lit)
	}
	if ev := w.Step(Left); ev != "turn" || w.D != 1 {
		t.Errorf("turn left wraps: got %s dir=%d", ev, w.D)
	}
	w.Step(Forward)
	if ev := w.Step(Activate); ev != "blocked" {
		t.Errorf("activate off pad: got %s", ev)
	}
}
