package stimres

import (
	"fmt"
	"strconv"
	"strings"
)

// effect is a parsed Receptron effect.
type effect struct {
	kind  string
	args  []string
	num   float64 // emit: intensity; set: ticks; add: delta
	same  bool    // emit: the delivered intensity
	extra map[string]any
	emit  *Source // emit: what to send
}

func (b *Broker[A, P]) parseEffect(s string) (effect, error) {
	f := strings.Fields(s)
	if len(f) == 0 {
		return effect{}, fmt.Errorf("empty effect")
	}
	e := effect{kind: f[0]}
	for _, a := range f[1:] {
		if k, v, ok := strings.Cut(a, "="); ok && e.kind == "emit" {
			if e.extra == nil {
				e.extra = map[string]any{}
			}
			e.extra[k] = parseValue(v)
			continue
		}
		e.args = append(e.args, a)
	}
	n := len(e.args)
	want := func(lo, hi int) error {
		if n < lo || n > hi {
			return fmt.Errorf("%s takes %d to %d arguments", e.kind, lo, hi)
		}
		return nil
	}
	num := func(s string) (float64, error) { return strconv.ParseFloat(s, 64) }
	var err error
	switch e.kind {
	case "emit":
		if err = want(2, 5); err != nil {
			return e, err
		}
		if _, ok := b.stims[e.args[0]]; !ok {
			return e, fmt.Errorf("no stim %q", e.args[0])
		}
		src := &Source{Stim: e.args[0]}
		if e.args[1] == "same" {
			e.same = true
		} else if src.Intensity, err = num(e.args[1]); err != nil {
			return e, err
		}
		if n > 2 {
			src.Medium = e.args[2]
		}
		if n > 3 {
			if src.Range, err = num(e.args[3]); err != nil {
				return e, err
			}
		}
		if n > 4 {
			src.Falloff = Falloff(e.args[4])
		}
		e.emit = src
	case "set":
		if err = want(2, 3); err == nil && n == 3 {
			e.num, err = num(e.args[2])
		}
	case "add":
		if err = want(2, 2); err == nil {
			e.num, err = num(e.args[1])
		}
	case "destroy", "stop":
		err = want(0, 0)
	case "spawn":
		if err = want(1, 1); err == nil && b.arch[e.args[0]] == nil {
			err = fmt.Errorf("no archetype %q", e.args[0])
		}
	case "call":
		if err = want(1, 100); err == nil && b.responses[e.args[0]] == nil {
			err = fmt.Errorf("no response %q (Respond before Load)", e.args[0])
		}
	default:
		err = fmt.Errorf("no effect %q", e.kind)
	}
	return e, err
}

// run applies rc's effects to d; did is whether it reacted, stop whether
// it ended the delivery.
func (b *Broker[A, P]) run(rc *Receptron, d Delivery[A, P]) (did, stop bool) {
	r := d.To
	for _, e := range rc.effects {
		switch e.kind {
		case "emit":
			st := Stimulus[A, P]{Stim: e.emit.Stim, Intensity: e.emit.Intensity, Medium: e.emit.Medium, Range: e.emit.Range,
				Falloff: e.emit.Falloff, Origin: b.World.Locate(r), Source: &r, Extra: e.extra}
			if e.same {
				st.Intensity = d.Intensity
			}
			switch st.Medium {
			case "":
				st.Medium = d.St.Medium
			case "self":
				st.Target = &r
			}
			b.Enqueue(st)
		case "set":
			b.SetFor(r, e.args[0], parseValue(e.args[1]), int(e.num))
		case "add":
			b.Add(r, e.args[0], e.num)
		case "destroy":
			b.Destroy(r)
		case "spawn":
			at := b.World.Locate(r)
			b.World.Spawned(b.NewObject(e.args[0]), at)
		case "stop":
			return did, true
		case "call":
			if !b.responses[e.args[0]](b, d, e.args[1:]) {
				return did, false
			}
		}
		did = true
	}
	return did, false
}
