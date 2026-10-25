package stimres

import (
	"bytes"
	"fmt"
	"math"
	"slices"
	"strings"
	"testing"
)

// A toy 2D grid world: cells hold a terrain archetype, objects stand at points.
type cell = [2]int
type pos = [2]float64

type grid struct {
	terrain   map[cell]string
	at        map[ID]pos
	destroyed []Receiver[cell]
}

func (g *grid) ArchetypeAt(c cell) string { return g.terrain[c] }
func (g *grid) Locate(r Receiver[cell]) pos {
	if r.Obj != 0 {
		return g.at[r.Obj]
	}
	return pos{float64(r.Addr[0]) + 0.5, float64(r.Addr[1]) + 0.5}
}
func (g *grid) Spawned(id ID, at pos) { g.at[id] = at }
func (g *grid) Destroyed(r Receiver[cell]) {
	g.destroyed = append(g.destroyed, r)
	if r.Obj == 0 {
		delete(g.terrain, r.Addr)
	}
}

func (g *grid) EncodeAddr(c cell) string { return fmt.Sprint(c[0], c[1]) }
func (g *grid) DecodeAddr(s string) (c cell, err error) {
	_, err = fmt.Sscan(s, &c[0], &c[1])
	return
}
func (g *grid) EncodePos(p pos) string { return fmt.Sprint(p[0], p[1]) }
func (g *grid) DecodePos(s string) (p pos, err error) {
	_, err = fmt.Sscan(s, &p[0], &p[1])
	return
}

func dist(a, b pos) float64 { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

// radius reaches every object and cell centre within Range of the origin.
func radius(g *grid) Medium[cell, pos] {
	return MediumFunc[cell, pos](func(st Stimulus[cell, pos], yield func(Receiver[cell], float64)) {
		ids := make([]ID, 0, len(g.at))
		for id := range g.at {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		for _, id := range ids {
			if d := dist(st.Origin, g.at[id]); d <= st.Range {
				yield(Object[cell](id), d)
			}
		}
		r := int(math.Ceil(st.Range))
		for x := int(st.Origin[0]) - r; x <= int(st.Origin[0])+r; x++ {
			for y := int(st.Origin[1]) - r; y <= int(st.Origin[1])+r; y++ {
				c := cell{x, y}
				if d := dist(st.Origin, g.Locate(At(c))); g.terrain[c] != "" && d <= st.Range {
					yield(At(c), d)
				}
			}
		}
	})
}

const defs = `
[stim.impact]
[stim.pierce]
parent = "impact"
[stim.bash]
parent = "impact"
[stim.water]
[stim.oil]
[stim.heat]
[stim.fire]
parent = "heat"

[archetype.thing]
props = { name = "Thing" }

[archetype.torch]
parent = "thing"
props = { lit = true }
[[archetype.torch.receptron]]
stim = "water"
if = ["lit"]
effects = ["set lit false"]
[[archetype.torch.source]]
stim = "heat"
medium = "radius"
intensity = 2
range = 1
period = 2
if = ["lit"]

[archetype.barrel]
parent = "thing"
props = { liquid = "water", hp = 20 }
[[archetype.barrel.receptron]]
stim = "pierce"
min = 1
effects = ["call leak"]
[[archetype.barrel.receptron]]
stim = "impact"
min = 10
effects = ["add hp -10"]

[archetype.oil_barrel]
parent = "barrel"
meta = ["oil_filled"]

[meta.oil_filled]
props = { liquid = "oil" }

[archetype.floor]
[[archetype.floor.receptron]]
stim = "heat"
effects = ["add warmth 1"]

[archetype.oil_puddle]
parent = "floor"
[[archetype.oil_puddle.receptron]]
stim = "fire"
if = ["!burning"]
effects = ["set burning true 3", "emit fire 1 radius 1.1"]
[[archetype.oil_puddle.receptron]]
stim = "expired"
if = ["$prop == burning"]
effects = ["set burnt true"]

[archetype.glass]
[[archetype.glass.receptron]]
stim = "impact"
effects = ["destroy", "stop"]
[[archetype.glass.receptron]]
stim = "impact"
effects = ["set never true"]
`

func setup(t *testing.T) (*Broker[cell, pos], *grid) {
	t.Helper()
	g := &grid{terrain: map[cell]string{}, at: map[ID]pos{}}
	b := New[cell, pos](g)
	b.Medium("radius", radius(g))
	b.Respond("leak", func(b *Broker[cell, pos], d Delivery[cell, pos], _ []string) bool {
		b.Enqueue(Stimulus[cell, pos]{Stim: b.String(d.To, "liquid"), Intensity: 1, Medium: "radius", Range: 1.5, Origin: g.Locate(d.To)})
		return true
	})
	if err := b.Load(strings.NewReader(defs)); err != nil {
		t.Fatal(err)
	}
	return b, g
}

func place(b *Broker[cell, pos], g *grid, arch string, at pos) Receiver[cell] {
	id := b.NewObject(arch)
	g.at[id] = at
	return Object[cell](id)
}

func TestChainDousesTorchBesideAPiercedBarrel(t *testing.T) {
	b, g := setup(t)
	barrel := place(b, g, "barrel", pos{0, 0})
	torch := place(b, g, "torch", pos{1, 0})
	got := b.Stimulate(Stimulus[cell, pos]{Stim: "pierce", Intensity: 5, Medium: "radius", Range: 0.1})
	if !slices.Equal(got, []Receiver[cell]{barrel}) {
		t.Fatalf("direct reactions %v, want only the barrel", got)
	}
	if b.Bool(torch, "lit") {
		t.Fatal("the barrel's water did not douse the torch")
	}
	if b.String(torch, "name") != "Thing" {
		t.Fatal("torch did not inherit name from thing")
	}
}

func TestThresholdAndStimHierarchy(t *testing.T) {
	b, g := setup(t)
	barrel := place(b, g, "barrel", pos{0, 0})
	b.Stimulate(Stimulus[cell, pos]{Stim: "bash", Intensity: 9, Medium: "radius", Range: 0.1})
	if b.Float(barrel, "hp") != 20 {
		t.Fatal("a bash under 10 got through")
	}
	b.Stimulate(Stimulus[cell, pos]{Stim: "bash", Intensity: 12, Medium: "radius", Range: 0.1})
	if b.Float(barrel, "hp") != 10 {
		t.Fatal("a bash is an impact: the impact receptron should take 10 hp")
	}
}

func TestMetapropertyOverridesArchetype(t *testing.T) {
	b, g := setup(t)
	oil := place(b, g, "oil_barrel", pos{0, 0})
	if b.String(oil, "liquid") != "oil" || !b.IsA(oil, "barrel") || !b.IsA(oil, "oil_filled") {
		t.Fatal("oil_barrel should be a barrel full of oil")
	}
	b.Set(oil, "liquid", "water")
	if b.String(oil, "liquid") != "water" {
		t.Fatal("an object override beats its metaproperty")
	}
}

func TestTimedPropertyOnCellExpires(t *testing.T) {
	b, g := setup(t)
	g.terrain[cell{0, 0}] = "oil_puddle"
	c := At(cell{0, 0})
	b.Stimulate(Stimulus[cell, pos]{Stim: "fire", Intensity: 1, Target: &c})
	if !b.Bool(c, "burning") {
		t.Fatal("oil did not catch")
	}
	for range 3 {
		b.Step()
	}
	if b.Bool(c, "burning") || !b.Bool(c, "burnt") {
		t.Fatal("burning should expire into burnt")
	}
	g.terrain[cell{0, 0}] = "floor"
	if b.Bool(c, "burnt") {
		t.Fatal("a cell that changed archetype keeps the old state")
	}
}

func TestFireLoopCarriesOverTheBudget(t *testing.T) {
	b, g := setup(t)
	g.terrain[cell{0, 0}], g.terrain[cell{1, 0}], g.terrain[cell{3, 0}] = "oil_puddle", "oil_puddle", "oil_puddle"
	g.terrain[cell{2, 0}] = "oil_puddle"
	b.Budget = 1
	c := At(cell{0, 0})
	b.Stimulate(Stimulus[cell, pos]{Stim: "fire", Intensity: 1, Target: &c})
	if b.Bool(At(cell{3, 0}), "burning") {
		t.Fatal("the budget should hold the spread back")
	}
	for range 3 {
		b.Step()
	}
	if !b.Bool(At(cell{3, 0}), "burning") {
		t.Fatal("what was left over should be delivered on later ticks")
	}
}

func TestPeriodicSourceOnlyWhileConditionHolds(t *testing.T) {
	b, g := setup(t)
	torch := place(b, g, "torch", pos{0.5, 0.5})
	g.terrain[cell{0, 0}] = "floor"
	c := At(cell{0, 0})
	for range 4 { // period 2: ticks 2 and 4
		b.Step()
	}
	if b.Float(c, "warmth") != 2 {
		t.Fatalf("warmth %v after 4 ticks, want 2", b.Float(c, "warmth"))
	}
	b.Set(torch, "lit", false)
	for range 4 {
		b.Step()
	}
	if b.Float(c, "warmth") != 2 {
		t.Fatal("an unlit torch gave off heat")
	}
}

func TestStopEndsTheDelivery(t *testing.T) {
	b, g := setup(t)
	g.terrain[cell{0, 0}] = "glass"
	b.Stimulate(Stimulus[cell, pos]{Stim: "bash", Intensity: 1, Origin: pos{0.5, 0.5}, Medium: "radius", Range: 0.1})
	if len(g.destroyed) != 1 || g.terrain[cell{0, 0}] != "" {
		t.Fatal("glass did not shatter")
	}
}

func TestSaveRestore(t *testing.T) {
	b, g := setup(t)
	torch := place(b, g, "torch", pos{1, 0})
	g.terrain[cell{0, 0}] = "oil_puddle"
	c := At(cell{0, 0})
	b.SetFor(c, "burning", true, 5)
	b.Set(torch, "lit", false)
	b.Enqueue(Stimulus[cell, pos]{Stim: "water", Intensity: 1, Medium: "radius", Range: 2, Origin: pos{3, 4}, Extra: map[string]any{"n": 2}})
	var buf bytes.Buffer
	if err := b.Save(&buf, g); err != nil {
		t.Fatal(err)
	}
	b2, _ := setup(t)
	b2.World = g
	if err := b2.Restore(&buf, g); err != nil {
		t.Fatal(err)
	}
	if b2.Bool(torch, "lit") || b2.Left(c, "burning") != 5 || len(b2.queue) != 1 ||
		b2.queue[0].Origin != (pos{3, 4}) || b2.queue[0].Extra["n"] != 2.0 {
		t.Fatalf("restored state differs:\n%s", buf.String())
	}
	if id := b2.NewObject("torch"); id != torch.Obj+1 {
		t.Fatal("IDs must continue after a restore")
	}
}

func TestLoadRejectsBadReferences(t *testing.T) {
	g := &grid{}
	b := New[cell, pos](g)
	err := b.Load(strings.NewReader(`
[archetype.a]
parent = "nope"
[[archetype.a.receptron]]
stim = "ghost"
effects = ["call missing", "frobnicate"]
`))
	for _, want := range []string{`parent archetype "nope"`, `no stim "ghost"`, `no response "missing"`, `no effect "frobnicate"`} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error %q, got %v", want, err)
		}
	}
}

func TestSelfSourceAndClear(t *testing.T) {
	g := &grid{terrain: map[cell]string{}, at: map[ID]pos{}}
	b := New[cell, pos](g)
	if err := b.Load(strings.NewReader(`
[stim.drip]
[archetype.tap]
[[archetype.tap.source]]
stim = "drip"
medium = "self"
period = 1
[[archetype.tap.receptron]]
stim = "drip"
if = ["wet"]
effects = ["clear wet"]
[[archetype.tap.receptron]]
stim = "drip"
if = ["!wet"]
effects = ["set wet true", "stop"]
`)); err != nil {
		t.Fatal(err)
	}
	tap := Object[cell](b.NewObject("tap"))
	b.Step()
	if !b.Bool(tap, "wet") {
		t.Fatal("a self source reaches its own receiver")
	}
	b.Step()
	if b.Bool(tap, "wet") {
		t.Fatal("clear drops the override")
	}
}
