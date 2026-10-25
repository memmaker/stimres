// Package stimres is a stimulus–response system in the manner of the Dark
// Engine's Act/React: every effect on a world is a Stimulus that a Broker
// carries through a game-defined Medium to the Receptrons of whatever it
// reaches. Receivers are objects with IDs or addressed receivers (such as
// grid cells) whose archetype the game works out from their address.
//
// The library does no geometry. A is the game's address type (a cell),
// P its position type; Mediums and the World translate between them.
package stimres

import (
	"fmt"
	"math"
)

// ID is an object's ID; 0 is no object.
type ID int

// Receiver is an object (Obj != 0) or an addressed receiver (Addr).
type Receiver[A comparable] struct {
	Obj  ID
	Addr A
}

// Object is the receiver for object id.
func Object[A comparable](id ID) Receiver[A] { return Receiver[A]{Obj: id} }

// At is the addressed receiver at a.
func At[A comparable](a A) Receiver[A] { return Receiver[A]{Addr: a} }

// Falloff is how intensity weakens with a Medium's distance.
type Falloff string

const (
	FalloffNone          Falloff = ""               // full intensity throughout
	FalloffLinear        Falloff = "linear"         // to 0 at Range
	FalloffInverseSquare Falloff = "inverse_square" // I / (1 + d²)
	FalloffStep          Falloff = "step"           // full within Range, else nothing
)

func (f Falloff) apply(i, d, rng float64) float64 {
	switch f {
	case FalloffLinear:
		if rng <= 0 {
			return i
		}
		return i * math.Max(0, 1-d/rng)
	case FalloffInverseSquare:
		return i / (1 + d*d)
	case FalloffStep:
		if d > rng {
			return 0
		}
	}
	return i
}

// Stimulus is one emission of a Stim.
type Stimulus[A comparable, P any] struct {
	Stim      string
	Intensity float64
	Medium    string // how it travels; ignored when Target is set
	Range     float64
	Falloff   Falloff
	Origin    P
	Source    *Receiver[A]   // who emitted it, if anyone
	Target    *Receiver[A]   // delivered to this receiver alone, at distance 0
	Extra     map[string]any // game data such as "head" or "sound"
}

// Delivery is a Stimulus as it reaches one receiver.
type Delivery[A comparable, P any] struct {
	St        Stimulus[A, P]
	To        Receiver[A]
	Intensity float64 // after falloff
	Dist      float64
}

// Medium finds the receivers a Stimulus reaches and how far each is,
// in whatever metric its Falloff should apply to.
type Medium[A comparable, P any] interface {
	Reach(st Stimulus[A, P], yield func(to Receiver[A], dist float64))
}

// MediumFunc adapts a function to Medium.
type MediumFunc[A comparable, P any] func(st Stimulus[A, P], yield func(to Receiver[A], dist float64))

func (f MediumFunc[A, P]) Reach(st Stimulus[A, P], yield func(Receiver[A], float64)) { f(st, yield) }

// World is what the library asks of the game.
type World[A comparable, P any] interface {
	ArchetypeAt(a A) string  // the archetype at an address, "" for none
	Locate(r Receiver[A]) P  // where a receiver is, as the origin of what it emits
	Spawned(id ID, at P)     // the spawn effect made object id
	Destroyed(r Receiver[A]) // the destroy effect removed r (its state is gone already)
}

// Response is a named game-defined effect. Returning false stops the
// Receptron's remaining effects (and, as its first effect, means it did not react).
type Response[A comparable, P any] func(b *Broker[A, P], d Delivery[A, P], args []string) bool

// Expired is the Stim a receiver gets when one of its timed properties
// runs out; Extra["prop"] names the property.
const Expired = "expired"

// Trace reports one Receptron that a Delivery passed (its band and conditions).
type Trace[A comparable, P any] struct {
	Tick      int
	D         Delivery[A, P]
	Receptron string // "archetype_or_meta:stim#index"
	Reacted   bool
}

// Broker carries every Stimulus to the Receptrons it reaches.
type Broker[A comparable, P any] struct {
	World  World[A, P]
	Budget int // queued deliveries per tick; 0 is unlimited. The rest carry over.
	Trace  func(Trace[A, P])

	mediums   map[string]Medium[A, P]
	responses map[string]Response[A, P]
	stims     map[string]string // stim -> parent
	arch      map[string]*def
	meta      map[string]*def

	objs  map[ID]*state
	cells map[A]*state
	order []Receiver[A] // receivers with state, oldest first: deterministic ticking
	next  ID

	queue    []Stimulus[A, P]
	tick     int
	used     int
	draining bool
}

// New makes a broker for world.
func New[A comparable, P any](world World[A, P]) *Broker[A, P] {
	return &Broker[A, P]{
		World:     world,
		mediums:   map[string]Medium[A, P]{},
		responses: map[string]Response[A, P]{},
		stims:     map[string]string{Expired: ""},
		arch:      map[string]*def{},
		meta:      map[string]*def{},
		objs:      map[ID]*state{},
		cells:     map[A]*state{},
	}
}

// Medium registers m under id.
func (b *Broker[A, P]) Medium(id string, m Medium[A, P]) { b.mediums[id] = m }

// Respond registers a named Response for the call effect. Register them
// before Load, which checks every call names one.
func (b *Broker[A, P]) Respond(name string, r Response[A, P]) { b.responses[name] = r }

// Tick is the broker's tick count.
func (b *Broker[A, P]) Tick() int { return b.tick }

// Stimulate delivers st now and drains the queue, reporting the receivers
// that reacted to st itself (not to what their reactions emitted). Called
// from a Response, it delivers st and leaves the queue to the drain under way.
func (b *Broker[A, P]) Stimulate(st Stimulus[A, P]) []Receiver[A] {
	reacted := b.deliver(st)
	if !b.draining {
		b.drain()
	}
	return reacted
}

// Enqueue queues st for delivery in order.
func (b *Broker[A, P]) Enqueue(st Stimulus[A, P]) { b.queue = append(b.queue, st) }

// Step advances one tick: timed properties count down (and expire), periodic
// Sources emit, and the queue drains within the Budget.
func (b *Broker[A, P]) Step() {
	b.tick++
	b.used = 0
	live, seen := b.order[:0], map[Receiver[A]]bool{}
	for _, r := range b.order { // drop the gone, and an address's older entry
		if !seen[r] && b.peek(r) != nil {
			seen[r] = true
			live = append(live, r)
		}
	}
	b.order = live
	for _, r := range b.order {
		s := b.peek(r)
		if s == nil {
			continue
		}
		for _, name := range sortedKeys(s.props) {
			p := s.props[name]
			if p.ticks == 0 {
				continue
			}
			if p.ticks--; p.ticks == 0 {
				if p.hasPrev {
					s.props[name] = &prop{v: p.prev}
				} else {
					delete(s.props, name)
				}
				r := r
				b.Enqueue(Stimulus[A, P]{Stim: Expired, Intensity: 1, Target: &r, Origin: b.World.Locate(r), Extra: map[string]any{"prop": name}})
			}
		}
	}
	for _, r := range b.order {
		for _, d := range b.chain(r) {
			for _, src := range d.Source {
				if src.Period > 0 && b.tick%src.Period == 0 && b.holds(src.conds, r, nil) {
					b.Enqueue(b.emission(r, src))
				}
			}
		}
	}
	b.drain()
}

// Emit fires r's (non-periodic) Source of stim now, if it has one and its
// conditions hold, reporting who reacted.
func (b *Broker[A, P]) Emit(r Receiver[A], stim string) []Receiver[A] {
	for _, d := range b.chain(r) {
		for _, src := range d.Source {
			if src.Stim == stim && b.holds(src.conds, r, nil) {
				return b.Stimulate(b.emission(r, src))
			}
		}
	}
	return nil
}

func (b *Broker[A, P]) emission(r Receiver[A], src *Source) Stimulus[A, P] {
	st := Stimulus[A, P]{Stim: src.Stim, Intensity: src.Intensity, Medium: src.Medium, Range: src.Range,
		Falloff: src.Falloff, Origin: b.World.Locate(r), Source: &r, Extra: src.Extra}
	if src.Medium == "self" {
		st.Target = &r
	}
	return st
}

func (b *Broker[A, P]) drain() {
	b.draining = true
	defer func() { b.draining = false }()
	for len(b.queue) > 0 && (b.Budget == 0 || b.used < b.Budget) {
		st := b.queue[0]
		b.queue = b.queue[1:]
		b.used++
		b.deliver(st)
	}
}

// deliver gives st to everything it reaches and reports who reacted.
func (b *Broker[A, P]) deliver(st Stimulus[A, P]) []Receiver[A] {
	type hit struct {
		to   Receiver[A]
		dist float64
	}
	var hits []hit
	if st.Target != nil {
		hits = append(hits, hit{*st.Target, 0})
	} else {
		m := b.mediums[st.Medium]
		if m == nil {
			panic(fmt.Sprintf("stimres: no medium %q for stim %q", st.Medium, st.Stim))
		}
		m.Reach(st, func(to Receiver[A], dist float64) { hits = append(hits, hit{to, dist}) })
	}
	var reacted []Receiver[A]
	for _, h := range hits { // reactions may change the world: the Medium has finished first
		d := Delivery[A, P]{St: st, To: h.to, Intensity: st.Falloff.apply(st.Intensity, h.dist, st.Range), Dist: h.dist}
		if b.receive(d) {
			reacted = append(reacted, h.to)
		}
	}
	return reacted
}

// receive runs the receptrons d passes, the most derived archetype and
// stim first, until one stops.
func (b *Broker[A, P]) receive(d Delivery[A, P]) bool {
	chain := b.chain(d.To)
	reacted := false
	for _, df := range chain {
		for stim := d.St.Stim; stim != ""; stim = b.stims[stim] {
			for i, rc := range df.Receptron {
				if rc.Stim != stim || !b.exists(d.To) {
					continue
				}
				if d.Intensity < rc.Min || rc.Max > 0 && d.Intensity > rc.Max || !b.holds(rc.conds, d.To, d.St.Extra) {
					continue
				}
				did, stop := b.run(rc, d)
				reacted = reacted || did
				if b.Trace != nil {
					b.Trace(Trace[A, P]{Tick: b.tick, D: d, Receptron: fmt.Sprintf("%s:%s#%d", df.id, stim, i), Reacted: did})
				}
				if stop {
					return reacted
				}
			}
		}
	}
	return reacted
}

// IsStim reports whether stim is kind or descends from it.
func (b *Broker[A, P]) IsStim(stim, kind string) bool {
	for ; stim != ""; stim = b.stims[stim] {
		if stim == kind {
			return true
		}
	}
	return false
}
