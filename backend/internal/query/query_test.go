package query

import (
	"reflect"
	"strings"
	"testing"
)

func TestCompile(t *testing.T) {
	cases := []struct {
		name string
		in   Expr
		want string
	}{
		{"nil", nil, ""},
		{"bare phrase", &Phrase{Text: "auth"}, "auth"},
		{"multiword phrase", &Phrase{Text: "auth failure"}, `"auth failure"`},
		{"phrase quote inside", &Phrase{Text: `quote"inside`}, `"quote\"inside"`},
		{"phrase backslash", &Phrase{Text: `back\slash`}, `"back\\slash"`},
		{"keyword phrase", &Phrase{Text: "AND"}, `"AND"`},
		{"eq bare", &Filter{Field: "host", Op: OpEq, Value: "h1"}, "host:h1"},
		{"eq default op", &Filter{Field: "host", Value: "h1"}, "host:h1"},
		{"eq space forces quotes", &Filter{Field: "host", Op: OpEq, Value: "web server"}, `host:"web server"`},
		{"eq colon forces quotes", &Filter{Field: "host", Op: OpEq, Value: "a:b"}, `host:"a:b"`},
		{"eq empty value", &Filter{Field: "host", Op: OpEq, Value: ""}, `host:""`},
		{"neq", &Filter{Field: "app", Op: OpNeq, Value: "vpn"}, "app:!=vpn"},
		{"match regex", &Filter{Field: "host", Op: OpMatch, Value: "f.*u"}, `host:~"f.*u"`},
		{"match bare regex", &Filter{Field: "host", Op: OpMatch, Value: "f.u"}, "host:~f.u"},
		{"gt", &Filter{Field: "severity", Op: OpGt, Value: "3"}, "severity:>3"},
		{"ge", &Filter{Field: "severity", Op: OpGe, Value: "3"}, "severity:>=3"},
		{"lt", &Filter{Field: "severity", Op: OpLt, Value: "3"}, "severity:<3"},
		{"le", &Filter{Field: "severity", Op: OpLe, Value: "3"}, "severity:<=3"},
		{"exists", &Filter{Field: "app", Op: OpExists}, "app:*"},
		{"and", &And{L: &Phrase{Text: "auth"}, R: &Filter{Field: "host", Op: OpEq, Value: "h8"}}, "auth AND host:h8"},
		{"or", &Or{L: &Filter{Field: "severity", Op: OpEq, Value: "2"}, R: &Filter{Field: "severity", Op: OpEq, Value: "3"}}, "severity:2 OR severity:3"},
		{"not", &Not{E: &Filter{Field: "severity", Op: OpEq, Value: "2"}}, "NOT (severity:2)"},
		{"or under and needs parens", &And{
			L: &Or{L: &Filter{Field: "host", Op: OpEq, Value: "h1"}, R: &Filter{Field: "host", Op: OpEq, Value: "h2"}},
			R: &Filter{Field: "app", Op: OpEq, Value: "colon"},
		}, "(host:h1 OR host:h2) AND app:colon"},
		{"and under or needs no parens", &Or{
			L: &And{L: &Filter{Field: "host", Op: OpEq, Value: "h1"}, R: &Filter{Field: "app", Op: OpEq, Value: "vpn"}},
			R: &Filter{Field: "severity", Op: OpEq, Value: "2"},
		}, "host:h1 AND app:vpn OR severity:2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Compile(c.in)
			if err != nil {
				t.Fatalf("Compile(%+v): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Compile = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCompileRejects(t *testing.T) {
	cases := []struct {
		name string
		in   Expr
	}{
		{"invalid field space", &Filter{Field: "host name", Op: OpEq, Value: "x"}},
		{"invalid field quote", &Filter{Field: `evil"`, Op: OpEq, Value: "x"}},
		{"invalid field paren", &Filter{Field: "a)b", Op: OpEq, Value: "x"}},
		{"invalid field star", &Filter{Field: "a*", Op: OpEq, Value: "x"}},
		{"invalid field colon", &Filter{Field: "a:b", Op: OpEq, Value: "x"}},
		{"empty field", &Filter{Field: "", Op: OpEq, Value: "x"}},
		{"unknown op", &Filter{Field: "host", Op: "wat", Value: "x"}},
		{"unknown expr", bogusExpr{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := Compile(c.in); err == nil {
				t.Errorf("Compile(%+v) = nil error, want rejection", c.in)
			}
		})
	}
}

type bogusExpr struct{}

func (bogusExpr) exprTag() {}

// Every compiled output must keep hostile values inert: they end up inside
// a properly closed quoted string or as bare-safe bytes, never as LogsQL
// syntax.
func TestCompileInjectionCorpus(t *testing.T) {
	hostile := []string{
		`a" OR severity:1`, `x" | stats count()`, `", "DROP TABLE`, `a)b`,
		`a)b OR NOT c`, `*`, `*:*`, `a%20b`, `100%`, `\\`, `"`, `(((`,
		`NOT (`, ` AND `, `ünïcode`, `tab\there`, `a:b:c`, "\n", "a\nb",
	}
	for _, v := range hostile {
		e := &Filter{Field: "host", Op: OpEq, Value: v}
		got, err := Compile(e)
		if err != nil {
			t.Fatalf("Compile(value %q): %v", v, err)
		}
		if !strings.HasPrefix(got, `host:`) {
			t.Fatalf("Compile(value %q) = %q, want host: prefix", v, got)
		}
		// Round-trip must recover the exact value (quoted values at least).
		back, err := Parse(got)
		if err != nil {
			t.Fatalf("Parse(%q): %v", got, err)
		}
		f, ok := back.(*Filter)
		if !ok || f.Field != "host" || f.Op != OpEq || f.Value != v {
			t.Errorf("value %q round-trip → %+v, want identical filter", v, back)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want Expr
	}{
		{"bare word", "auth", &Phrase{Text: "auth"}},
		{"quoted phrase", `"auth failure"`, &Phrase{Text: "auth failure"}},
		{"filter", "host:h1", &Filter{Field: "host", Op: OpEq, Value: "h1"}},
		{"filter quoted", `host:"web server"`, &Filter{Field: "host", Op: OpEq, Value: "web server"}},
		{"neq", "app:!=vpn", &Filter{Field: "app", Op: OpNeq, Value: "vpn"}},
		{"match", `host:~"f.*u"`, &Filter{Field: "host", Op: OpMatch, Value: "f.*u"}},
		{"gt", "severity:>3", &Filter{Field: "severity", Op: OpGt, Value: "3"}},
		{"ge", "severity:>=3", &Filter{Field: "severity", Op: OpGe, Value: "3"}},
		{"exists", "app:*", &Filter{Field: "app", Op: OpExists}},
		{"explicit and", "auth AND host:h8", &And{L: &Phrase{Text: "auth"}, R: &Filter{Field: "host", Op: OpEq, Value: "h8"}}},
		{"implicit and", "auth host:h8", &And{L: &Phrase{Text: "auth"}, R: &Filter{Field: "host", Op: OpEq, Value: "h8"}}},
		{"or", "a OR b", &Or{L: &Phrase{Text: "a"}, R: &Phrase{Text: "b"}}},
		{"not", "NOT a", &Not{E: &Phrase{Text: "a"}}},
		{"group", "(a OR b) AND c", &And{
			L: &Or{L: &Phrase{Text: "a"}, R: &Phrase{Text: "b"}},
			R: &Phrase{Text: "c"},
		}},
		{"not value is phrase", "NOTICE", &Phrase{Text: "NOTICE"}},
		{"keyword value quoted", `app:"NOTICE"`, &Filter{Field: "app", Op: OpEq, Value: "NOTICE"}},
		{"multi colon value", "host:a:b", &Filter{Field: "host", Op: OpEq, Value: "a:b"}},
		{"wildcard value not exists", "host:*foo", &Filter{Field: "host", Op: OpEq, Value: "*foo"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Parse(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{
		"", "   ", "a AND", "a OR", "(a", "a)", `host:"unterminated`, "host:", "NOT",
	} {
		if _, err := Parse(in); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", in)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	exprs := []Expr{
		&Phrase{Text: "auth"},
		&Phrase{Text: "auth failure"},
		&Phrase{Text: `quote"inside`},
		&Phrase{Text: `back\slash`},
		&Phrase{Text: "AND"},
		&Filter{Field: "host", Op: OpEq, Value: "h1"},
		&Filter{Field: "host", Op: OpEq, Value: "web server"},
		&Filter{Field: "host", Op: OpEq, Value: `weird"value`},
		&Filter{Field: "app", Op: OpNeq, Value: "vpn"},
		&Filter{Field: "host", Op: OpMatch, Value: "f.*u"},
		&Filter{Field: "severity", Op: OpGt, Value: "3"},
		&Filter{Field: "severity", Op: OpLe, Value: "3"},
		&Filter{Field: "app", Op: OpExists},
		&And{L: &Phrase{Text: "auth"}, R: &Filter{Field: "host", Op: OpEq, Value: "h8"}},
		&Or{L: &Filter{Field: "severity", Op: OpEq, Value: "2"}, R: &Filter{Field: "severity", Op: OpEq, Value: "3"}},
		&Not{E: &Filter{Field: "severity", Op: OpEq, Value: "2"}},
		&And{
			L: &Or{L: &Filter{Field: "host", Op: OpEq, Value: "h1"}, R: &Filter{Field: "host", Op: OpEq, Value: "h2"}},
			R: &Filter{Field: "app", Op: OpEq, Value: "colon"},
		},
		&Or{
			L: &And{L: &Phrase{Text: "auth"}, R: &Filter{Field: "host", Op: OpEq, Value: "h8"}},
			R: &Not{E: &Filter{Field: "severity", Op: OpEq, Value: "2"}},
		},
	}
	for _, e := range exprs {
		text, err := Compile(e)
		if err != nil {
			t.Fatalf("Compile(%#v): %v", e, err)
		}
		back, err := Parse(text)
		if err != nil {
			t.Fatalf("Parse(%q): %v", text, err)
		}
		if !reflect.DeepEqual(back, e) {
			t.Errorf("round-trip: %#v → %q → %#v", e, text, back)
		}
		// Compiling the reparsed AST must be a fixed point too.
		again, err := Compile(back)
		if err != nil {
			t.Fatalf("Compile(reparsed %q): %v", text, err)
		}
		if again != text {
			t.Errorf("recompile: %q → %q", text, again)
		}
	}
}

// Anything a hostile client can submit must either error or compile to a
// safely escaped filter; the corpus includes LogsQL pipe and keyword
// injection attempts.
func TestParseCompileHostileInput(t *testing.T) {
	inputs := []string{
		`* | stats count()`, `host:* | delete`, `a OR b) AND c`, `"a" "b"`,
		`host:~"x) | stats"`, `severity:>"3 OR host:admin"`, `field:"`,
		`a:b:c:d`, `NOT NOT NOT a`, `((((a))))`, `*`, `host:%20`,
	}
	for _, in := range inputs {
		e, err := Parse(in)
		if err != nil {
			continue // rejected input is safe
		}
		out, err := Compile(e)
		if err != nil {
			continue // rejected AST is safe
		}
		if strings.Contains(stripQuotes(out), "|") {
			t.Errorf("input %q compiled to %q with a pipe outside quotes", in, out)
		}
		if l, r := strings.Count(stripQuotes(out), "("), strings.Count(stripQuotes(out), ")"); l != r {
			t.Errorf("input %q compiled to %q with unbalanced parens outside quotes", in, out)
		}
	}
}

// stripQuotes removes quoted segments (honoring \" so a quote cannot be
// smuggled out), leaving only syntax that could escape the value position.
func stripQuotes(s string) string {
	var b strings.Builder
	inQuote := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote {
			if c == '\\' {
				i++
			} else if c == '"' {
				inQuote = false
			}
			continue
		}
		if c == '"' {
			inQuote = true
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}
