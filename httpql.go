package httpql

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Kind int

const (
	IntKind Kind = iota
	TextKind
	BoolKind
	TimeKind
)

// Field is a field of R a query can test.
type Field[R any] struct {
	kind Kind
	raw  bool
	get  func(R) any
}

func Int[R any](get func(R) int64) Field[R] {
	return Field[R]{kind: IntKind, get: func(r R) any { return get(r) }}
}

func Text[R any](get func(R) string) Field[R] {
	return Field[R]{kind: TextKind, get: func(r R) any { return get(r) }}
}

// Texts is a field holding several values: a clause holds when one of them
// passes it, or, for a negated operator (ne, ncont...), when all of them do.
func Texts[R any](get func(R) []string) Field[R] {
	return Field[R]{kind: TextKind, get: func(r R) any { return get(r) }}
}

func Bool[R any](get func(R) bool) Field[R] {
	return Field[R]{kind: BoolKind, get: func(r R) any { return get(r) }}
}

func Time[R any](get func(R) time.Time) Field[R] {
	return Field[R]{kind: TimeKind, get: func(r R) any { return get(r) }}
}

// Raw marks a field read from the raw messages: a query testing it is costly,
// see Query.NeedsRaw.
func (f Field[R]) Raw() Field[R] { f.raw = true; return f }

func (f Field[R]) Kind() Kind { return f.kind }

// Schema is the language of an app: its fields, by name.
type Schema[R any] struct {
	Fields map[string]Field[R]
	// Refused are Caido's fields (or namespaces) that mean nothing to the app,
	// with the reason given to whoever asks for them.
	Refused map[string]string
	// Bare are the text fields a lone string searches in: req.raw and resp.raw
	// when empty.
	Bare []string
}

var ops = map[Kind][]string{
	IntKind:  {"eq", "ne", "gt", "gte", "lt", "lte"},
	TextKind: {"eq", "ne", "cont", "ncont", "like", "nlike", "regex", "nregex"},
	BoolKind: {"eq", "ne"},
	TimeKind: {"gt", "lt"},
}

type Query[R any] struct {
	root node[R]
}

// Match tells whether r passes the query; an empty query passes everything.
func (q *Query[R]) Match(r R) bool {
	if q == nil || q.root == nil {
		return true
	}
	return q.root.eval(r)
}

// NeedsRaw reports whether the query reads raw requests or responses.
func (q *Query[R]) NeedsRaw() bool { return q != nil && q.root != nil && q.root.raw() }

type node[R any] interface {
	eval(R) bool
	raw() bool
}

type and[R any] struct{ l, r node[R] }
type or[R any] struct{ l, r node[R] }

func (n and[R]) eval(r R) bool { return n.l.eval(r) && n.r.eval(r) }
func (n and[R]) raw() bool     { return n.l.raw() || n.r.raw() }
func (n or[R]) eval(r R) bool  { return n.l.eval(r) || n.r.eval(r) }
func (n or[R]) raw() bool      { return n.l.raw() || n.r.raw() }

type clause[R any] struct {
	f    Field[R]
	op   string
	ival int64
	sval string
	bval bool
	tval time.Time
	re   *regexp.Regexp
}

func (c clause[R]) raw() bool { return c.f.raw }

func (c clause[R]) eval(r R) bool {
	switch v := c.f.get(r).(type) {
	case int64:
		switch c.op {
		case "eq":
			return v == c.ival
		case "ne":
			return v != c.ival
		case "gt":
			return v > c.ival
		case "gte":
			return v >= c.ival
		case "lt":
			return v < c.ival
		case "lte":
			return v <= c.ival
		}
	case bool:
		return (v == c.bval) == (c.op == "eq")
	case time.Time:
		if v.IsZero() {
			return false
		}
		if c.op == "gt" {
			return v.After(c.tval)
		}
		return v.Before(c.tval)
	case string:
		return c.str(v)
	case []string:
		neg := strings.HasPrefix(c.op, "n")
		for _, s := range v {
			if c.str(s) != neg {
				return !neg
			}
		}
		return neg
	}
	return false
}

func (c clause[R]) str(v string) bool {
	switch c.op {
	case "eq":
		return v == c.sval
	case "ne":
		return v != c.sval
	case "cont":
		return strings.Contains(strings.ToLower(v), strings.ToLower(c.sval))
	case "ncont":
		return !strings.Contains(strings.ToLower(v), strings.ToLower(c.sval))
	case "like", "regex":
		return c.re.MatchString(v)
	case "nlike", "nregex":
		return !c.re.MatchString(v)
	}
	return false
}

// likeRegexp turns a SQL LIKE pattern (% any run, _ one character) into a
// case-insensitive regexp.
func likeRegexp(p string) *regexp.Regexp {
	var b strings.Builder
	b.WriteString("(?is)^")
	for _, r := range p {
		switch r {
		case '%':
			b.WriteString(".*")
		case '_':
			b.WriteString(".")
		default:
			b.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	b.WriteString("$")
	return regexp.MustCompile(b.String())
}

// timeLayouts are the dates a time clause takes; without a zone, local time.
var timeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}

func parseTime(s string) (time.Time, bool) {
	for _, l := range timeLayouts {
		if t, err := time.ParseInLocation(l, s, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func (s *Schema[R]) refusal(name string) (string, bool) {
	if why, ok := s.Refused[name]; ok {
		return fmt.Sprintf("%s: not available here, %s", name, why), true
	}
	for k, why := range s.Refused {
		if strings.HasPrefix(name, k+".") {
			return fmt.Sprintf("%s: not available here, %s", k, why), true
		}
	}
	return "", false
}

func (s *Schema[R]) clause(name, op string, val token) (clause[R], error) {
	if msg, ok := s.refusal(name); ok {
		return clause[R]{}, fmt.Errorf("%s", msg)
	}
	f, ok := s.Fields[name]
	if !ok {
		return clause[R]{}, fmt.Errorf("unknown field %q", name)
	}
	valid := false
	for _, o := range ops[f.kind] {
		valid = valid || o == op
	}
	if !valid {
		return clause[R]{}, fmt.Errorf("%s: unknown operator %q (%s)", name, op, strings.Join(ops[f.kind], ", "))
	}
	c := clause[R]{f: f, op: op}
	switch f.kind {
	case IntKind:
		n, err := strconv.ParseInt(val.text, 10, 64)
		if err != nil || val.quoted {
			return clause[R]{}, fmt.Errorf("%s.%s: %q is not an integer", name, op, val.text)
		}
		c.ival = n
	case BoolKind:
		b, err := strconv.ParseBool(val.text)
		if err != nil {
			return clause[R]{}, fmt.Errorf("%s.%s: %q is not true or false", name, op, val.text)
		}
		c.bval = b
	case TimeKind:
		t, ok := parseTime(val.text)
		if !ok {
			return clause[R]{}, fmt.Errorf("%s.%s: %q is not a date (2006-01-02, 2006-01-02T15:04:05Z07:00)", name, op, val.text)
		}
		c.tval = t
	case TextKind:
		c.sval = val.text
		switch op {
		case "like", "nlike":
			c.re = likeRegexp(val.text)
		case "regex", "nregex":
			re, err := regexp.Compile(val.text)
			if err != nil {
				return clause[R]{}, fmt.Errorf("%s.%s: %w", name, op, err)
			}
			c.re = re
		}
	}
	return c, nil
}
