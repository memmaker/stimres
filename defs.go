package stimres

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// data is one definitions file:
//
//	[stim.pierce]
//	parent = "impact"
//
//	[archetype.oil_barrel]
//	parent = "barrel"
//	meta = ["oil_filled"]
//	props = { name = "Oil barrel", lit = false }
//
//	[[archetype.barrel.receptron]]
//	stim = "pierce"
//	min = 1
//	effects = ["emit water 4 radius 1", "call leak"]
type data struct {
	Stim      map[string]StimDef `toml:"stim"`
	Archetype map[string]*def    `toml:"archetype"`
	Meta      map[string]*def    `toml:"meta"`
}

// StimDef places a Stim in the stim hierarchy.
type StimDef struct {
	Parent string `toml:"parent"`
}

// def is an archetype or a metaproperty.
type def struct {
	id        string
	Parent    string         `toml:"parent"` // archetypes only
	Meta      []string       `toml:"meta"`   // archetypes only
	Props     map[string]any `toml:"props"`
	Receptron []*Receptron   `toml:"receptron"`
	Source    []*Source      `toml:"source"`
}

// Receptron reacts to a Stim (or one descending from it) whose delivered
// intensity is within [Min, Max] (Max 0: no limit) when its If conditions
// hold, by running Effects in order.
//
// A condition is "prop", "!prop", or "prop OP value" with OP one of
// == != < <= > >=; "$key" in place of prop reads the stimulus's Extra.
//
// Effects:
//
//	emit STIM INTENSITY|same [MEDIUM|self] [RANGE] [FALLOFF] [key=value...]
//	set PROP VALUE [TICKS]
//	add PROP DELTA
//	clear PROP              (back to the definitions' value)
//	destroy
//	spawn ARCHETYPE
//	stop                    (no further receptrons for this delivery)
//	call RESPONSE [ARGS...] (a false result skips the rest)
type Receptron struct {
	Stim    string   `toml:"stim"`
	Min     float64  `toml:"min"`
	Max     float64  `toml:"max"`
	If      []string `toml:"if"`
	Effects []string `toml:"effects"`

	conds   []cond
	effects []effect
}

// Source is what a receiver emits: periodically (Period ticks) while its
// If conditions hold, or on Emit when Period is 0. Medium "self" delivers
// it to the receiver alone.
type Source struct {
	Stim      string         `toml:"stim"`
	Medium    string         `toml:"medium"`
	Intensity float64        `toml:"intensity"`
	Range     float64        `toml:"range"`
	Falloff   Falloff        `toml:"falloff"`
	Period    int            `toml:"period"`
	If        []string       `toml:"if"`
	Extra     map[string]any `toml:"extra"`

	conds []cond
}

// Load reads definition files, adding to what is loaded, and checks that
// every reference resolves.
func (b *Broker[A, P]) Load(files ...io.Reader) error {
	for _, f := range files {
		var d data
		dec := toml.NewDecoder(f)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			return err
		}
		for id, s := range d.Stim {
			if _, dup := b.stims[id]; dup {
				return fmt.Errorf("stim %s defined twice", id)
			}
			b.stims[id] = s.Parent
		}
		for kind, defs := range map[string]map[string]*def{"archetype": d.Archetype, "meta": d.Meta} {
			into := b.arch
			if kind == "meta" {
				into = b.meta
			}
			for id, df := range defs {
				if into[id] != nil {
					return fmt.Errorf("%s %s defined twice", kind, id)
				}
				df.id = id
				for k, v := range df.Props {
					df.Props[k] = normalize(v)
				}
				for k, v := range df.Source {
					for x, e := range v.Extra {
						df.Source[k].Extra[x] = normalize(e)
					}
				}
				into[id] = df
			}
		}
	}
	return b.check()
}

func (b *Broker[A, P]) check() error {
	var errs []error
	stim := func(where, s string) {
		if _, ok := b.stims[s]; !ok {
			errs = append(errs, fmt.Errorf("%s: no stim %q", where, s))
		}
	}
	for id, p := range b.stims {
		if _, ok := b.stims[p]; p != "" && !ok {
			errs = append(errs, fmt.Errorf("stim %s: no parent %q", id, p))
		}
	}
	for kind, defs := range map[string]map[string]*def{"archetype": b.arch, "meta": b.meta} {
		for id, df := range defs {
			where := kind + " " + id
			if df.Parent != "" && b.arch[df.Parent] == nil {
				errs = append(errs, fmt.Errorf("%s: no parent archetype %q", where, df.Parent))
			}
			if kind == "meta" && (df.Parent != "" || len(df.Meta) > 0) {
				errs = append(errs, fmt.Errorf("%s: a metaproperty has no parent or metas", where))
			}
			for _, m := range df.Meta {
				if b.meta[m] == nil {
					errs = append(errs, fmt.Errorf("%s: no metaproperty %q", where, m))
				}
			}
			for i, rc := range df.Receptron {
				w := fmt.Sprintf("%s receptron %d", where, i)
				stim(w, rc.Stim)
				var err error
				if rc.conds, err = parseConds(rc.If); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", w, err))
				}
				rc.effects = rc.effects[:0]
				for _, e := range rc.Effects {
					ef, err := b.parseEffect(e)
					if err != nil {
						errs = append(errs, fmt.Errorf("%s: %q: %w", w, e, err))
					}
					rc.effects = append(rc.effects, ef)
				}
			}
			for i, src := range df.Source {
				w := fmt.Sprintf("%s source %d", where, i)
				stim(w, src.Stim)
				var err error
				if src.conds, err = parseConds(src.If); err != nil {
					errs = append(errs, fmt.Errorf("%s: %w", w, err))
				}
			}
		}
	}
	for id := range b.arch { // a parent loop would hang every lookup
		seen := map[string]bool{}
		for a := b.arch[id]; a != nil; a = b.arch[a.Parent] {
			if seen[a.id] {
				errs = append(errs, fmt.Errorf("archetype %s: parent loop", id))
				break
			}
			seen[a.id] = true
		}
	}
	return errors.Join(errs...)
}

// cond is a parsed condition.
type cond struct {
	name  string
	extra bool // reads the stimulus's Extra
	neg   bool // "!prop"
	op    string
	val   any
}

var ops = []string{"==", "!=", "<=", ">=", "<", ">"}

func parseConds(src []string) ([]cond, error) {
	var out []cond
	for _, s := range src {
		f := strings.Fields(s)
		var c cond
		switch len(f) {
		case 1:
			c.name, c.neg = strings.CutPrefix(f[0], "!")
		case 3:
			if !contains(ops, f[1]) {
				return nil, fmt.Errorf("condition %q: no operator %q", s, f[1])
			}
			c.name, c.op, c.val = f[0], f[1], parseValue(f[2])
		default:
			return nil, fmt.Errorf("condition %q: want PROP or PROP OP VALUE", s)
		}
		c.name, c.extra = strings.CutPrefix(c.name, "$")
		out = append(out, c)
	}
	return out, nil
}

func (b *Broker[A, P]) holds(cs []cond, r Receiver[A], extra map[string]any) bool {
	for _, c := range cs {
		var v any
		if c.extra {
			v = extra[c.name]
		} else {
			v, _ = b.Get(r, c.name)
		}
		if !c.test(v) {
			return false
		}
	}
	return true
}

func (c cond) test(v any) bool {
	if c.op == "" {
		return truthy(v) != c.neg
	}
	switch c.op {
	case "==":
		return v == c.val
	case "!=":
		return v != c.val
	}
	x, ok1 := v.(float64)
	y, ok2 := c.val.(float64)
	if !ok1 || !ok2 {
		return false
	}
	switch c.op {
	case "<":
		return x < y
	case "<=":
		return x <= y
	case ">":
		return x > y
	}
	return x >= y
}

func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		return x != ""
	}
	return false
}

// parseValue reads a bool, a number or else a string (quotes optional).
func parseValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return strings.Trim(s, `"`)
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if x == y {
			return true
		}
	}
	return false
}
