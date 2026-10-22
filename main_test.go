package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/hajimehoshi/ebiten/v2"

	"bytewise/engine"
)

func newTestGame(t *testing.T) *Game {
	t.Helper()
	g, err := newGame(false)
	if err != nil {
		t.Fatal(err)
	}
	g.savePath = filepath.Join(t.TempDir(), "save.json")
	g.save = saveData{Programs: map[string]map[string][]string{}}
	g.loadLevel(0)
	return g
}

// stepUntilDone advances the simulation (without reading real input) until
// the run finishes or the frame budget is exhausted.
func stepUntilDone(t *testing.T, g *Game, maxFrames int) {
	t.Helper()
	for i := 0; i < maxFrames; i++ {
		g.time += dt
		if !g.running {
			return
		}
		if g.anim != nil {
			g.anim.t += dt * float64(g.speed) / g.anim.dur
			if g.anim.t < 1 {
				continue
			}
			g.anim = nil
			if g.world.Won() {
				g.finish("win")
				return
			}
		}
		instr, ok := g.interp.Next()
		if !ok {
			g.finish("fail")
			return
		}
		if _, isCall := engine.Calls[instr.Cmd]; isCall {
			g.anim = &anim{kind: "call", dur: baseStep * 0.3}
			continue
		}
		g.anim = &anim{kind: g.world.Step(instr.Cmd), dur: baseStep}
	}
	t.Fatal("run did not finish")
}

func TestKeyboardSolvesFirstLevel(t *testing.T) {
	g := newTestGame(t)
	for _, k := range []ebiten.Key{ebiten.KeyArrowUp, ebiten.KeyArrowUp, ebiten.KeyArrowUp, ebiten.KeyA} {
		g.onKey(k)
	}
	if got := g.programs["main"]; len(got) != 4 || got[3] != engine.Activate {
		t.Fatalf("program = %v", got)
	}
	g.onKey(ebiten.KeyJ) // not in this level's palette
	if len(g.programs["main"]) != 4 {
		t.Fatal("disallowed command was inserted")
	}
	g.onKey(ebiten.KeySpace)
	if !g.running {
		t.Fatal("space should start the run")
	}
	stepUntilDone(t, g, 60*30)
	if g.result != "win" {
		t.Fatalf("result = %q", g.result)
	}
	data, err := os.ReadFile(g.savePath)
	if err != nil {
		t.Fatal(err)
	}
	var s saveData
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	if len(s.Completed) != 1 || s.Completed[0] != 0 || len(s.Programs["0"]["main"]) != 4 {
		t.Fatalf("save = %+v", s)
	}
	g.onKey(ebiten.KeyEnter)
	if g.levelIndex != 1 || g.result != "" {
		t.Fatalf("enter after win should go to level 2, got %d %q", g.levelIndex+1, g.result)
	}
}

func TestFailAndOverflowResults(t *testing.T) {
	g := newTestGame(t)
	g.onKey(ebiten.KeyArrowUp)
	g.onKey(ebiten.KeySpace)
	stepUntilDone(t, g, 60*30)
	if g.result != "fail" {
		t.Fatalf("result = %q", g.result)
	}
	g.onKey(ebiten.KeyEscape)
	if g.result != "" || g.running {
		t.Fatal("escape should reset")
	}

	g.loadLevel(9) // Recursion level: main=P1, p1 may call itself
	g.programs["main"] = []string{engine.P1}
	g.programs["p1"] = []string{engine.Left, engine.P1}
	g.startRun()
	for g.running {
		g.Update()
	}
	if g.result != "overflow" {
		t.Fatalf("result = %q", g.result)
	}
}

func TestMouseDragAndClick(t *testing.T) {
	g := newTestGame(t)
	pal := g.paletteRects[0] // Forward
	px, py := pal.rect.Min.X+5, pal.rect.Min.Y+5

	// click a palette tile: appended to the selected program
	g.onMouseDown(px, py)
	g.onMouseUp(px, py)
	if got := g.programs["main"]; len(got) != 1 || got[0] != engine.Forward {
		t.Fatalf("click should append, got %v", got)
	}

	// drag the Activate tile onto slot 0: inserted before Forward
	act := g.paletteRects[3]
	slot0 := g.boxes["main"].slots[0]
	g.onMouseDown(act.rect.Min.X+5, act.rect.Min.Y+5)
	g.onMouseMove(act.rect.Min.X+40, act.rect.Min.Y+40)
	if g.drag == nil || !g.drag.moved {
		t.Fatal("drag should have started")
	}
	g.onMouseUp(slot0.Min.X+5, slot0.Min.Y+5)
	if got := g.programs["main"]; len(got) != 2 || got[0] != engine.Activate || got[1] != engine.Forward {
		t.Fatalf("drag should insert at slot 0, got %v", got)
	}

	// drag the placed Forward (slot 1) into empty space of the box: moves to end (no-op here)
	slot1 := g.boxes["main"].slots[1]
	box := g.boxes["main"].box
	g.onMouseDown(slot1.Min.X+5, slot1.Min.Y+5)
	g.onMouseMove(slot1.Min.X+40, slot1.Min.Y+40)
	g.onMouseUp(box.Max.X-2, box.Min.Y+2)
	if got := g.programs["main"]; len(got) != 2 || got[1] != engine.Forward {
		t.Fatalf("drag to box end, got %v", got)
	}

	// drag a placed tile off the panel: removed
	g.onMouseDown(slot0.Min.X+5, slot0.Min.Y+5)
	g.onMouseMove(slot0.Min.X-40, slot0.Min.Y+40)
	g.onMouseUp(100, 300)
	if got := g.programs["main"]; len(got) != 1 || got[0] != engine.Forward {
		t.Fatalf("drag off should remove, got %v", got)
	}

	// click a placed tile: removed
	g.onMouseDown(slot0.Min.X+5, slot0.Min.Y+5)
	g.onMouseUp(slot0.Min.X+5, slot0.Min.Y+5)
	if len(g.programs["main"]) != 0 {
		t.Fatal("click on placed tile should remove it")
	}

	// a full program rejects inserts and puts a moved tile back
	g.loadLevel(6)
	g.programs["main"] = nil
	for i := 0; i < g.level.Limits["main"]; i++ {
		g.programs["main"] = append(g.programs["main"], engine.P1)
	}
	full := len(g.programs["main"])
	g.programs["p1"] = []string{engine.Forward}
	g.selected = "p1"
	g.onKey(ebiten.KeyArrowUp)
	if len(g.programs["p1"]) != 2 {
		t.Fatal("key insert into p1 failed")
	}
	s := g.boxes["p1"].slots[0]
	target := g.boxes["main"].slots[0]
	g.onMouseDown(s.Min.X+5, s.Min.Y+5)
	g.onMouseMove(s.Min.X+40, s.Min.Y+40)
	g.onMouseUp(target.Min.X+5, target.Min.Y+5)
	if len(g.programs["main"]) != full || len(g.programs["p1"]) != 2 {
		t.Fatalf("full target should bounce back: main=%v p1=%v", g.programs["main"], g.programs["p1"])
	}
}

func TestLevelSelectAndNavigation(t *testing.T) {
	g := newTestGame(t)
	g.navButtons[2].action() // Next >
	if g.levelIndex != 1 {
		t.Fatal("next level")
	}
	g.navButtons[0].action()
	g.navButtons[0].action() // wraps to the last level
	if g.levelIndex != len(g.levels)-1 {
		t.Fatalf("prev should wrap, got %d", g.levelIndex)
	}
	g.navButtons[1].action()
	if !g.showLevels {
		t.Fatal("levels overlay")
	}
	r := g.levelGridRects()[2]
	g.onMouseDown(r.Min.X+1, r.Min.Y+1)
	if g.showLevels || g.levelIndex != 2 {
		t.Fatalf("choose level 3, got %d", g.levelIndex)
	}
	g.onKey(ebiten.KeyTab)
	if g.selected != "main" { // level 3 has only main
		t.Fatal("tab with one program")
	}
	g.cycleSpeed()
	g.cycleSpeed()
	if g.speed != 4 || g.buttons[2].label != "Speed 4x" {
		t.Fatalf("speed %d %q", g.speed, g.buttons[2].label)
	}
}

func TestPaletteHidesProceduresWithoutSlots(t *testing.T) {
	g := newTestGame(t)
	for i, level := range g.levels {
		g.loadLevel(i)
		for _, p := range g.palette() {
			if callee, ok := engine.Calls[p]; ok && level.Limits[callee] == 0 {
				t.Errorf("level %d shows %s without a %s box", i+1, p, callee)
			}
		}
		if len(g.visiblePrograms()) == 0 {
			t.Errorf("level %d has no programs", i+1)
		}
	}
}
