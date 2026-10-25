package stimres

import (
	"cmp"
	"maps"
	"slices"
)

// state is a receiver's own part: an object's archetype and metaproperties,
// and the property overrides of an object or an addressed receiver.
type state struct {
	arch  string
	metas []string
	props map[string]*prop
}

// prop is an override; a timed one (ticks > 0) reverts to prev.
type prop struct {
	v       any
	ticks   int
	prev    any
	hasPrev bool
}

// NewObject makes an object of archetype arch with metaproperties metas.
func (b *Broker[A, P]) NewObject(arch string, metas ...string) ID {
	if b.arch[arch] == nil {
		panic("stimres: no archetype " + arch)
	}
	b.next++
	b.objs[b.next] = &state{arch: arch, metas: metas, props: map[string]*prop{}}
	b.order = append(b.order, Object[A](b.next))
	return b.next
}

// Destroy removes r and its state, and tells the World.
func (b *Broker[A, P]) Destroy(r Receiver[A]) {
	if r.Obj != 0 {
		delete(b.objs, r.Obj)
	} else {
		delete(b.cells, r.Addr)
	}
	b.World.Destroyed(r)
}

// exists reports whether r is a live object or an address with an archetype.
func (b *Broker[A, P]) exists(r Receiver[A]) bool { return b.archOf(r) != "" }

// Arch is r's archetype, "" if there is none.
func (b *Broker[A, P]) Arch(r Receiver[A]) string { return b.archOf(r) }

func (b *Broker[A, P]) archOf(r Receiver[A]) string {
	if r.Obj != 0 {
		if s := b.objs[r.Obj]; s != nil {
			return s.arch
		}
		return ""
	}
	return b.World.ArchetypeAt(r.Addr)
}

// peek is r's state, nil if it has none. An address whose archetype has
// changed loses its state.
func (b *Broker[A, P]) peek(r Receiver[A]) *state {
	if r.Obj != 0 {
		return b.objs[r.Obj]
	}
	s := b.cells[r.Addr]
	if s != nil && s.arch != b.World.ArchetypeAt(r.Addr) {
		delete(b.cells, r.Addr)
		return nil
	}
	return s
}

// own is r's state, made for an address if need be; nil for a gone object
// or an address without an archetype.
func (b *Broker[A, P]) own(r Receiver[A]) *state {
	if s := b.peek(r); s != nil || r.Obj != 0 {
		return s
	}
	arch := b.World.ArchetypeAt(r.Addr)
	if arch == "" {
		return nil
	}
	s := &state{arch: arch, props: map[string]*prop{}}
	b.cells[r.Addr] = s
	b.order = append(b.order, r)
	return s
}

// chain is r's definitions, most derived first: its own metaproperties,
// then each archetype up the hierarchy followed by its metaproperties.
func (b *Broker[A, P]) chain(r Receiver[A]) []*def {
	var out []*def
	if s := b.peek(r); s != nil {
		for _, m := range s.metas {
			out = append(out, b.meta[m])
		}
	}
	for a := b.arch[b.archOf(r)]; a != nil; a = b.arch[a.Parent] {
		out = append(out, a)
		for _, m := range a.Meta {
			out = append(out, b.meta[m])
		}
	}
	return out
}

// IsA reports whether r's archetype is or descends from id, or r has metaproperty id.
func (b *Broker[A, P]) IsA(r Receiver[A], id string) bool {
	for _, d := range b.chain(r) {
		if d.id == id {
			return true
		}
	}
	return false
}

// AddMeta gives object r metaproperty m.
func (b *Broker[A, P]) AddMeta(r Receiver[A], m string) {
	if b.meta[m] == nil {
		panic("stimres: no metaproperty " + m)
	}
	if s := b.peek(r); s != nil && !slices.Contains(s.metas, m) {
		s.metas = append(s.metas, m)
	}
}

// RemoveMeta takes metaproperty m from object r.
func (b *Broker[A, P]) RemoveMeta(r Receiver[A], m string) {
	if s := b.peek(r); s != nil {
		s.metas = slices.DeleteFunc(s.metas, func(x string) bool { return x == m })
	}
}

// Get is r's property name: its override, else the most derived definition's.
func (b *Broker[A, P]) Get(r Receiver[A], name string) (any, bool) {
	if s := b.peek(r); s != nil {
		if p, ok := s.props[name]; ok {
			return p.v, true
		}
	}
	for _, d := range b.chain(r) {
		if v, ok := d.Props[name]; ok {
			return v, true
		}
	}
	return nil, false
}

// Float is property name as a number (0 if unset or not a number).
func (b *Broker[A, P]) Float(r Receiver[A], name string) float64 {
	v, _ := b.Get(r, name)
	f, _ := v.(float64)
	return f
}

// Bool is property name as a bool (false if unset or not a bool).
func (b *Broker[A, P]) Bool(r Receiver[A], name string) bool {
	v, _ := b.Get(r, name)
	t, _ := v.(bool)
	return t
}

// String is property name as a string ("" if unset or not a string).
func (b *Broker[A, P]) String(r Receiver[A], name string) string {
	v, _ := b.Get(r, name)
	s, _ := v.(string)
	return s
}

// Set overrides r's property name. Ints become float64.
func (b *Broker[A, P]) Set(r Receiver[A], name string, v any) { b.SetFor(r, name, v, 0) }

// SetFor overrides r's property name for ticks ticks (0: for good); then it
// reverts and r gets an Expired stimulus. Setting a running timed property
// again restarts it and keeps what it reverts to.
func (b *Broker[A, P]) SetFor(r Receiver[A], name string, v any, ticks int) {
	s := b.own(r)
	if s == nil {
		return
	}
	p := &prop{v: normalize(v), ticks: ticks}
	if old, ok := s.props[name]; ok && ticks > 0 {
		p.prev, p.hasPrev = old.v, true
		if old.ticks > 0 {
			p.prev, p.hasPrev = old.prev, old.hasPrev
		}
	}
	s.props[name] = p
}

// Add adds delta to r's numeric property name.
func (b *Broker[A, P]) Add(r Receiver[A], name string, delta float64) {
	v := b.Float(r, name) + delta
	if s := b.peek(r); s != nil {
		if p, ok := s.props[name]; ok && p.ticks > 0 {
			p.v = v // keeps its timer
			return
		}
	}
	b.Set(r, name, v)
}

// Clear drops r's override of name, back to its definitions' value.
func (b *Broker[A, P]) Clear(r Receiver[A], name string) {
	if s := b.peek(r); s != nil {
		delete(s.props, name)
	}
}

// Left is how many ticks r's timed property name has left (0: not timed).
func (b *Broker[A, P]) Left(r Receiver[A], name string) int {
	if s := b.peek(r); s != nil {
		if p, ok := s.props[name]; ok {
			return p.ticks
		}
	}
	return 0
}

// Addresses are the addressed receivers with state, oldest first.
func (b *Broker[A, P]) Addresses() []A {
	var out []A
	for _, r := range b.order {
		if r.Obj == 0 && b.peek(r) != nil {
			out = append(out, r.Addr)
		}
	}
	return out
}

func normalize(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case ID:
		return float64(x)
	case float32:
		return float64(x)
	}
	return v
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.SortedFunc(maps.Keys(m), cmp.Compare[string])
}
