package httpql

import (
	"fmt"
	"strings"
)

type tokKind int

const (
	tEOF tokKind = iota
	tWord
	tString
	tColon
	tLParen
	tRParen
)

type token struct {
	kind   tokKind
	text   string
	quoted bool
	pos    int
}

func lex(s string) ([]token, error) {
	var out []token
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c == '(':
			out = append(out, token{kind: tLParen, pos: i})
			i++
		case c == ')':
			out = append(out, token{kind: tRParen, pos: i})
			i++
		case c == ':':
			out = append(out, token{kind: tColon, pos: i})
			i++
		case c == '"' || c == '\'':
			var b strings.Builder
			j := i + 1
			for ; j < len(r) && r[j] != c; j++ {
				if r[j] == '\\' && j+1 < len(r) {
					j++
					switch r[j] {
					case 'n':
						b.WriteRune('\n')
					case 'r':
						b.WriteRune('\r')
					case 't':
						b.WriteRune('\t')
					default:
						if r[j] != c && r[j] != '\\' {
							b.WriteRune('\\')
						}
						b.WriteRune(r[j])
					}
					continue
				}
				b.WriteRune(r[j])
			}
			if j >= len(r) {
				return nil, fmt.Errorf("at %d: unterminated string", i+1)
			}
			out = append(out, token{kind: tString, text: b.String(), quoted: true, pos: i})
			i = j + 1
		default:
			j := i
			// After a colon a bare value runs to the next space or paren.
			afterColon := len(out) > 0 && out[len(out)-1].kind == tColon
			for j < len(r) && !strings.ContainsRune(" \t\r\n()", r[j]) && (afterColon || r[j] != ':') {
				j++
			}
			out = append(out, token{kind: tWord, text: string(r[i:j]), pos: i})
			i = j
		}
	}
	return append(out, token{kind: tEOF, pos: len(r)}), nil
}

type parser[R any] struct {
	s       *Schema[R]
	toks    []token
	i       int
	presets map[string]string
	depth   int // presets inside presets
}

func (p *parser[R]) peek() token { return p.toks[p.i] }
func (p *parser[R]) next() token {
	t := p.toks[p.i]
	if t.kind != tEOF {
		p.i++
	}
	return t
}

func isOp(t token, op string) bool { return t.kind == tWord && strings.EqualFold(t.text, op) }

// Parse reads a query; an empty one matches everything.
func (s *Schema[R]) Parse(q string) (*Query[R], error) { return s.ParsePresets(q, nil) }

// ParsePresets reads a query whose preset:"name" clauses stand for the
// queries of presets, by name.
func (s *Schema[R]) ParsePresets(q string, presets map[string]string) (*Query[R], error) {
	root, err := s.parse(q, presets, 0)
	if err != nil {
		return nil, err
	}
	return &Query[R]{root: root}, nil
}

func (s *Schema[R]) parse(q string, presets map[string]string, depth int) (node[R], error) {
	toks, err := lex(q)
	if err != nil {
		return nil, err
	}
	p := &parser[R]{s: s, toks: toks, presets: presets, depth: depth}
	if p.peek().kind == tEOF {
		return nil, nil
	}
	root, err := p.or()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tEOF {
		return nil, fmt.Errorf("at %d: unexpected %s", t.pos+1, describe(t))
	}
	return root, nil
}

func (p *parser[R]) preset(t token) (node[R], error) {
	if c := p.next(); c.kind != tColon {
		return nil, fmt.Errorf("at %d: expected \":\" after preset, got %s", c.pos+1, describe(c))
	}
	v := p.next()
	if v.kind != tWord && v.kind != tString {
		return nil, fmt.Errorf("at %d: preset needs a name", v.pos+1)
	}
	q, ok := p.presets[v.text]
	switch {
	case !ok:
		return nil, fmt.Errorf("at %d: no preset %q in filters", v.pos+1, v.text)
	case p.depth >= 8:
		return nil, fmt.Errorf("at %d: preset %q goes in circles", t.pos+1, v.text)
	}
	n, err := p.s.parse(q, p.presets, p.depth+1)
	switch {
	case err != nil:
		return nil, fmt.Errorf("preset %q: %w", v.text, err)
	case n == nil:
		return nil, fmt.Errorf("at %d: preset %q is empty", v.pos+1, v.text)
	}
	return n, nil
}

func describe(t token) string {
	switch t.kind {
	case tEOF:
		return "end of query"
	case tColon:
		return `":"`
	case tLParen:
		return `"("`
	case tRParen:
		return `")"`
	}
	return fmt.Sprintf("%q", t.text)
}

func (p *parser[R]) or() (node[R], error) {
	l, err := p.and()
	if err != nil {
		return nil, err
	}
	for isOp(p.peek(), "OR") {
		p.next()
		r, err := p.and()
		if err != nil {
			return nil, err
		}
		l = or[R]{l, r}
	}
	return l, nil
}

func (p *parser[R]) and() (node[R], error) {
	l, err := p.primary()
	if err != nil {
		return nil, err
	}
	for isOp(p.peek(), "AND") {
		p.next()
		r, err := p.primary()
		if err != nil {
			return nil, err
		}
		l = and[R]{l, r}
	}
	return l, nil
}

func (p *parser[R]) primary() (node[R], error) {
	t := p.next()
	switch t.kind {
	case tLParen:
		n, err := p.or()
		if err != nil {
			return nil, err
		}
		if c := p.next(); c.kind != tRParen {
			return nil, fmt.Errorf("at %d: expected \")\", got %s", c.pos+1, describe(c))
		}
		return n, nil
	case tString:
		return p.bare(t)
	case tWord:
		if isOp(t, "AND") || isOp(t, "OR") {
			return nil, fmt.Errorf("at %d: %s needs a clause before it", t.pos+1, strings.ToUpper(t.text))
		}
		if t.text == "preset" {
			return p.preset(t)
		}
		if msg, ok := p.s.refusal(t.text); ok {
			return nil, fmt.Errorf("at %d: %s", t.pos+1, msg)
		}
		dot := strings.LastIndex(t.text, ".")
		if dot < 0 {
			return nil, fmt.Errorf("at %d: expected namespace.field.operator:value, got %q", t.pos+1, t.text)
		}
		name, op := t.text[:dot], t.text[dot+1:]
		if c := p.next(); c.kind != tColon {
			return nil, fmt.Errorf("at %d: expected \":\" after %s, got %s", c.pos+1, t.text, describe(c))
		}
		v := p.next()
		if v.kind != tWord && v.kind != tString {
			return nil, fmt.Errorf("at %d: expected a value after %s:, got %s", v.pos+1, t.text, describe(v))
		}
		c, err := p.s.clause(name, op, v)
		if err != nil {
			return nil, fmt.Errorf("at %d: %w", t.pos+1, err)
		}
		return c, nil
	}
	return nil, fmt.Errorf("at %d: unexpected %s", t.pos+1, describe(t))
}

// bare is a lone string: it is searched in the Bare fields.
func (p *parser[R]) bare(t token) (node[R], error) {
	names := p.s.Bare
	if len(names) == 0 {
		names = []string{"req.raw", "resp.raw"}
	}
	var n node[R]
	for _, name := range names {
		c, err := p.s.clause(name, "cont", t)
		if err != nil {
			return nil, fmt.Errorf("at %d: a lone string searches %s: %w", t.pos+1, name, err)
		}
		if n == nil {
			n = c
		} else {
			n = or[R]{n, c}
		}
	}
	return n, nil
}
