package stimres

import (
	"io"

	"github.com/pelletier/go-toml/v2"
)

// Codec turns the game's address and position types into text for saves.
type Codec[A comparable, P any] interface {
	EncodeAddr(A) string
	DecodeAddr(string) (A, error)
	EncodePos(P) string
	DecodePos(string) (P, error)
}

type savedProp struct {
	V       any  `toml:"v"`
	Ticks   int  `toml:"ticks,omitempty"`
	Prev    any  `toml:"prev,omitempty"`
	HasPrev bool `toml:"has_prev,omitempty"`
}

type savedState struct {
	Obj   ID                   `toml:"obj,omitempty"`
	Addr  string               `toml:"addr,omitempty"`
	Arch  string               `toml:"arch,omitempty"`
	Metas []string             `toml:"metas,omitempty"`
	Props map[string]savedProp `toml:"props,omitempty"`
}

type savedRecv struct {
	Obj  ID     `toml:"obj,omitempty"`
	Addr string `toml:"addr,omitempty"`
}

type savedStim struct {
	Stim      string         `toml:"stim"`
	Intensity float64        `toml:"intensity"`
	Medium    string         `toml:"medium,omitempty"`
	Range     float64        `toml:"range,omitempty"`
	Falloff   Falloff        `toml:"falloff,omitempty"`
	Origin    string         `toml:"origin"`
	Source    *savedRecv     `toml:"source,omitempty"`
	Target    *savedRecv     `toml:"target,omitempty"`
	Extra     map[string]any `toml:"extra,omitempty"`
}

type saved struct {
	Tick     int          `toml:"tick"`
	Used     int          `toml:"used"`
	Next     ID           `toml:"next"`
	Receiver []savedState `toml:"receiver"`
	Queue    []savedStim  `toml:"queue"`
}

// Save writes the broker's state (not its definitions) as TOML.
func (b *Broker[A, P]) Save(w io.Writer, c Codec[A, P]) error {
	recv := func(r *Receiver[A]) *savedRecv {
		if r == nil {
			return nil
		}
		if r.Obj != 0 {
			return &savedRecv{Obj: r.Obj}
		}
		return &savedRecv{Addr: c.EncodeAddr(r.Addr)}
	}
	sv := saved{Tick: b.tick, Used: b.used, Next: b.next}
	seen := map[Receiver[A]]bool{}
	for _, r := range b.order {
		s := b.peek(r)
		if s == nil || seen[r] {
			continue
		}
		seen[r] = true
		ss := savedState{Obj: r.Obj, Metas: s.metas, Props: map[string]savedProp{}}
		if r.Obj != 0 {
			ss.Arch = s.arch
		} else {
			ss.Addr = c.EncodeAddr(r.Addr)
		}
		for k, p := range s.props {
			ss.Props[k] = savedProp{V: p.v, Ticks: p.ticks, Prev: p.prev, HasPrev: p.hasPrev}
		}
		sv.Receiver = append(sv.Receiver, ss)
	}
	for _, st := range b.queue {
		sv.Queue = append(sv.Queue, savedStim{Stim: st.Stim, Intensity: st.Intensity, Medium: st.Medium, Range: st.Range,
			Falloff: st.Falloff, Origin: c.EncodePos(st.Origin), Source: recv(st.Source), Target: recv(st.Target), Extra: st.Extra})
	}
	return toml.NewEncoder(w).Encode(sv)
}

// Restore replaces the broker's state with a saved one. Definitions,
// Mediums and Responses stay as they are.
func (b *Broker[A, P]) Restore(r io.Reader, c Codec[A, P]) error {
	var sv saved
	if err := toml.NewDecoder(r).Decode(&sv); err != nil {
		return err
	}
	recv := func(s *savedRecv) (*Receiver[A], error) {
		if s == nil {
			return nil, nil
		}
		if s.Obj != 0 {
			return &Receiver[A]{Obj: s.Obj}, nil
		}
		a, err := c.DecodeAddr(s.Addr)
		return &Receiver[A]{Addr: a}, err
	}
	b.tick, b.used, b.next = sv.Tick, sv.Used, sv.Next
	b.objs, b.cells, b.order, b.queue = map[ID]*state{}, map[A]*state{}, nil, nil
	for _, ss := range sv.Receiver {
		s := &state{arch: ss.Arch, metas: ss.Metas, props: map[string]*prop{}}
		for k, p := range ss.Props {
			s.props[k] = &prop{v: normalize(p.V), ticks: p.Ticks, prev: normalize(p.Prev), hasPrev: p.HasPrev}
		}
		if ss.Obj != 0 {
			b.objs[ss.Obj] = s
			b.order = append(b.order, Object[A](ss.Obj))
			continue
		}
		a, err := c.DecodeAddr(ss.Addr)
		if err != nil {
			return err
		}
		s.arch = b.World.ArchetypeAt(a)
		b.cells[a] = s
		b.order = append(b.order, At(a))
	}
	for _, q := range sv.Queue {
		o, err := c.DecodePos(q.Origin)
		if err != nil {
			return err
		}
		st := Stimulus[A, P]{Stim: q.Stim, Intensity: q.Intensity, Medium: q.Medium, Range: q.Range, Falloff: q.Falloff, Origin: o, Extra: q.Extra}
		for k, v := range st.Extra {
			st.Extra[k] = normalize(v)
		}
		if st.Source, err = recv(q.Source); err != nil {
			return err
		}
		if st.Target, err = recv(q.Target); err != nil {
			return err
		}
		b.queue = append(b.queue, st)
	}
	return nil
}
