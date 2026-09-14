// Package query defines the search/filter AST (docs/roadmap.md Phase 3) and
// compiles it to VictoriaLogs LogsQL. This package and the storage adapter
// are the only places LogsQL strings exist (ADR-0001); the UI's Visual mode
// serializes to this AST, Advanced mode parses LogsQL into it.
//
// Escaping follows VictoriaLogs semantics, verified against a live v1.52
// instance: values that are a single bare-safe token are emitted unquoted;
// everything else is double-quoted with \" and \\ escapes. Inside quotes,
// *, parens, %, spaces, and unicode are literal (no wildcard, no percent
// decoding). Unquoted * IS a wildcard, so it never appears bare.
package query

import (
	"fmt"
	"strings"
)

// Expr is the filter AST. It is deliberately small: everything the Phase 4
// UI's Visual mode can express.
type Expr interface {
	exprTag()
}

// And, Or, Not compose. AND binds tighter than OR, matching LogsQL.
type And struct{ L, R Expr }
type Or struct{ L, R Expr }
type Not struct{ E Expr }

// Filter is field:op:value. Ops: eq (default), neq, match (regex via
// LogsQL ~), gt/ge/lt/le (numeric or duration compare), exists.
type Filter struct {
	Field string
	Op    string
	Value string
}

// Phrase is a free-text match over the message field.
type Phrase struct{ Text string }

func (And) exprTag()    {}
func (Or) exprTag()     {}
func (Not) exprTag()    {}
func (Filter) exprTag() {}
func (Phrase) exprTag() {}

// Op constants.
const (
	OpEq     = "eq"
	OpNeq    = "neq"
	OpMatch  = "match"
	OpGt     = "gt"
	OpGe     = "ge"
	OpLt     = "lt"
	OpLe     = "le"
	OpExists = "exists"
)

// Compile renders the AST to one LogsQL filter expression (no pipes —
// callers append those). Every user value is either a bare-safe token or a
// properly quoted string, and field names are validated against a safe
// charset, so injection is impossible by construction (docs/security.md §5).
func Compile(e Expr) (string, error) {
	if e == nil {
		return "", nil
	}
	var b strings.Builder
	if err := compileTo(&b, e); err != nil {
		return "", err
	}
	return b.String(), nil
}

func compileTo(b *strings.Builder, e Expr) error {
	switch x := e.(type) {
	case *And:
		if err := child(b, x.L); err != nil {
			return err
		}
		b.WriteString(" AND ")
		return child(b, x.R)
	case *Or:
		if err := compileTo(b, x.L); err != nil {
			return err
		}
		b.WriteString(" OR ")
		return compileTo(b, x.R)
	case *Not:
		b.WriteString("NOT (")
		if err := compileTo(b, x.E); err != nil {
			return err
		}
		b.WriteByte(')')
		return nil
	case *Phrase:
		b.WriteString(phraseOut(x.Text))
		return nil
	case *Filter:
		return compileFilter(b, x)
	default:
		return fmt.Errorf("query: unknown expr %T", e)
	}
}

// child renders an And operand, parenthesizing Or: AND binds tighter than
// OR, so an Or under an And must be grouped or the meaning changes.
func child(b *strings.Builder, e Expr) error {
	if _, ok := e.(*Or); ok {
		b.WriteByte('(')
		if err := compileTo(b, e); err != nil {
			return err
		}
		b.WriteByte(')')
		return nil
	}
	return compileTo(b, e)
}

func compileFilter(b *strings.Builder, f *Filter) error {
	if err := validField(f.Field); err != nil {
		return err
	}
	switch f.Op {
	case "", OpEq:
		fmt.Fprintf(b, "%s:%s", f.Field, valueOut(f.Value))
	case OpNeq:
		fmt.Fprintf(b, "%s:!=%s", f.Field, valueOut(f.Value))
	case OpMatch:
		fmt.Fprintf(b, "%s:~%s", f.Field, valueOut(f.Value))
	case OpGt:
		fmt.Fprintf(b, "%s:>%s", f.Field, valueOut(f.Value))
	case OpGe:
		fmt.Fprintf(b, "%s:>=%s", f.Field, valueOut(f.Value))
	case OpLt:
		fmt.Fprintf(b, "%s:<%s", f.Field, valueOut(f.Value))
	case OpLe:
		fmt.Fprintf(b, "%s:<=%s", f.Field, valueOut(f.Value))
	case OpExists:
		fmt.Fprintf(b, "%s:*", f.Field)
	default:
		return fmt.Errorf("query: unknown op %q", f.Op)
	}
	return nil
}

func phraseOut(text string) string {
	return valueOut(text)
}

// valueOut renders a value as a bare token when that is unambiguous, and
// as a quoted string otherwise. Bare tokens are limited to a charset that
// excludes every LogsQL metacharacter (: * ! ~ > < ( ) " \ space % and
// friends), and the exact keywords AND/OR/NOT are always quoted.
func valueOut(v string) string {
	if isBare(v) {
		return v
	}
	return quote(v)
}

func isBare(v string) bool {
	if v == "" {
		return false
	}
	switch v {
	case "AND", "OR", "NOT":
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.' || c == '-' || c == '/' || c == '@' || c == '+' || c == '~'
		if !ok {
			return false
		}
	}
	return true
}

// quote wraps s in double quotes, escaping " and \ — the only two escapes
// LogsQL quoted strings define, and the same two that keep the string
// closed. For OpMatch values these escapes are also valid regex escapes
// for the literal characters, so regex semantics survive round-tripping.
func quote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// validField enforces the LogsQL field-name charset; anything outside it is
// rejected rather than escaped, because field names come from the data
// model and facet list, not free text.
func validField(f string) error {
	if f == "" {
		return fmt.Errorf("query: empty field")
	}
	for i := 0; i < len(f); i++ {
		c := f[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '_' || c == '.'
		if !ok {
			return fmt.Errorf("query: invalid field name %q", f)
		}
	}
	return nil
}
