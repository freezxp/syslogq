package query

import (
	"fmt"
	"strings"
)

// Parse converts an Advanced-mode LogsQL filter expression to the AST. It
// accepts the subset Compile emits (round-trip safe) plus the conveniences
// a human expects: implicit AND between adjacent terms, quoted phrases, and
// lowercase-free keywords. Filter field names are NOT validated here —
// Compile is the single enforcement point, and every caller must pass the
// parsed AST through Compile (or Equal) before it reaches storage.
func Parse(s string) (Expr, error) {
	p := &parser{src: s}
	e, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	p.ws()
	if p.pos < len(p.src) {
		return nil, fmt.Errorf("query: unexpected %q", p.src[p.pos:])
	}
	return e, nil
}

type parser struct {
	src string
	pos int
}

func (p *parser) parseOr() (Expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if !p.keyword("OR") {
			return l, nil
		}
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = &Or{L: l, R: r}
	}
}

// parseAnd chains unary terms with explicit AND or a bare space (implicit
// AND, matching LogsQL). It stops at end, ')', or an upcoming OR so parseOr
// can claim it.
func (p *parser) parseAnd() (Expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		p.ws()
		if p.pos >= len(p.src) || p.peek() == ')' || p.keywordPeek("OR") {
			return l, nil
		}
		p.keyword("AND") // optional separator
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = &And{L: l, R: r}
	}
}

func (p *parser) parseUnary() (Expr, error) {
	p.ws()
	if p.keyword("NOT") {
		e, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &Not{E: e}, nil
	}
	if p.peek() == '(' {
		p.pos++
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		p.ws()
		if p.peek() != ')' {
			return nil, fmt.Errorf("query: missing closing parenthesis")
		}
		p.pos++
		return e, nil
	}
	return p.parseTerm()
}

func (p *parser) parseTerm() (Expr, error) {
	p.ws()
	if p.peek() == '"' {
		v, err := p.quoted()
		if err != nil {
			return nil, err
		}
		return &Phrase{Text: v}, nil
	}
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == ')' || c == '(' {
			break
		}
		if c == ':' {
			return p.parseFilter(p.src[start:p.pos])
		}
		p.pos++
	}
	if p.pos == start {
		return nil, fmt.Errorf("query: empty term")
	}
	return &Phrase{Text: p.src[start:p.pos]}, nil
}

func (p *parser) parseFilter(field string) (Expr, error) {
	p.pos++ // consume ':'
	op := OpEq
	switch {
	case p.hasPrefix("!="):
		op, p.pos = OpNeq, p.pos+2
	case p.hasPrefix("~"):
		op, p.pos = OpMatch, p.pos+1
	case p.hasPrefix(">="):
		op, p.pos = OpGe, p.pos+2
	case p.hasPrefix("<="):
		op, p.pos = OpLe, p.pos+2
	case p.hasPrefix(">"):
		op, p.pos = OpGt, p.pos+1
	case p.hasPrefix("<"):
		op, p.pos = OpLt, p.pos+1
	}
	if op == OpEq && p.peek() == '*' {
		after := p.pos + 1
		if after >= len(p.src) || p.src[after] == ' ' || p.src[after] == ')' {
			p.pos = after
			return &Filter{Field: field, Op: OpExists}, nil
		}
	}
	if p.peek() == '"' {
		v, err := p.quoted()
		if err != nil {
			return nil, err
		}
		return &Filter{Field: field, Op: op, Value: v}, nil
	}
	start := p.pos
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == ')' || c == '(' {
			break
		}
		p.pos++
	}
	v := p.src[start:p.pos]
	if v == "" {
		return nil, fmt.Errorf("query: missing value after field %s", field)
	}
	return &Filter{Field: field, Op: op, Value: v}, nil
}

func (p *parser) quoted() (string, error) {
	p.pos++ // opening quote
	var b strings.Builder
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == '\\' && p.pos+1 < len(p.src) {
			b.WriteByte(p.src[p.pos+1])
			p.pos += 2
			continue
		}
		if c == '"' {
			p.pos++
			return b.String(), nil
		}
		b.WriteByte(c)
		p.pos++
	}
	return "", fmt.Errorf("query: unterminated quote")
}

// keyword consumes kw only when it is followed by a delimiter, so NOTICE
// never eats NOT. It returns whether the keyword was consumed.
func (p *parser) keyword(kw string) bool {
	if !p.keywordPeek(kw) {
		return false
	}
	p.pos += len(kw)
	return true
}

func (p *parser) keywordPeek(kw string) bool {
	if !p.hasPrefix(kw) {
		return false
	}
	after := p.pos + len(kw)
	if after < len(p.src) {
		c := p.src[after]
		if c != ' ' && c != '(' && c != ')' {
			return false
		}
	}
	return true
}

func (p *parser) hasPrefix(s string) bool {
	return strings.HasPrefix(p.src[p.pos:], s)
}

func (p *parser) peek() byte {
	if p.pos < len(p.src) {
		return p.src[p.pos]
	}
	return 0
}

func (p *parser) ws() {
	for p.pos < len(p.src) && p.src[p.pos] == ' ' {
		p.pos++
	}
}
