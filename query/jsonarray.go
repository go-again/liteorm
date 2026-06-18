package query

import (
	"encoding/json"
	"slices"
	"strings"

	"liteorm.org/dialect"
)

// This file adds typed JSON and array predicates. JSON path extraction (-> /
// ->>) works on both Postgres and SQLite; jsonb containment (@>) and the array
// operators are Postgres-only and are gated by FeatJSONB / FeatArray, so building
// them against another dialect fails at build time instead of producing opaque
// SQL. Path keys and values are always bound parameters — never interpolated —
// so a key like `a'); drop table` is harmless.

// JSON names a JSON/JSONB column for path extraction and containment. Drill into
// nested keys with Key, then compare the extracted text (Eq/Ne/Like/In) or test
// containment (Contains).
func JSON(name string) JSONPath { return JSONPath{col: name} }

// JSONPath is a JSON column plus a (possibly empty) object-key path.
type JSONPath struct {
	col  string
	keys []string
}

// Key drills one level into an object. Chain Key calls to walk nested objects:
// JSON("data").Key("address").Key("city").Eq("Paris").
func (p JSONPath) Key(k string) JSONPath {
	return JSONPath{col: p.col, keys: append(slices.Clone(p.keys), k)}
}

// extractText renders the column walked to the path as TEXT: the inner keys use
// -> and the final key uses ->>, with every key a bound parameter. With no keys
// it is just the quoted column.
func (p JSONPath) extractText(d dialect.Dialect) (string, []any) {
	var b strings.Builder
	b.WriteString(quoteCol(d, p.col))
	args := make([]any, 0, len(p.keys))
	for i, k := range p.keys {
		if i == len(p.keys)-1 {
			b.WriteString(" ->> ?")
		} else {
			b.WriteString(" -> ?")
		}
		args = append(args, k)
	}
	return b.String(), args
}

func (p JSONPath) textCmp(op string, v any) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			expr, args := p.extractText(d)
			return expr + " " + op + " ?", append(args, v)
		},
		cols: []string{p.col},
	}
}

// Eq/Ne/Like compare the text value at the path. In matches any of several.
func (p JSONPath) Eq(v any) Predicate        { return p.textCmp("=", v) }
func (p JSONPath) Ne(v any) Predicate        { return p.textCmp("<>", v) }
func (p JSONPath) Like(pat string) Predicate { return p.textCmp("LIKE", pat) }

// In matches the path's text value against a set.
func (p JSONPath) In(vs ...any) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			if len(vs) == 0 {
				return "1=0", nil // empty IN set is always false
			}
			expr, args := p.extractText(d)
			var b strings.Builder
			b.WriteString(expr)
			b.WriteString(" IN (")
			for i := range vs {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteByte('?')
			}
			b.WriteByte(')')
			return b.String(), append(args, vs...)
		},
		cols: []string{p.col},
	}
}

// Contains renders the Postgres jsonb containment operator (col @> value). The
// value is JSON-encoded and bound. Postgres only (FeatJSONB).
func (p JSONPath) Contains(value any) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			enc, _ := json.Marshal(value)
			return quoteCol(d, p.col) + " @> ?", []any{string(enc)}
		},
		cols: []string{p.col},
		feat: dialect.FeatJSONB,
	}
}

// ArrayColumn is a Postgres array column of element type E. Its operators are
// Postgres-only (FeatArray).
type ArrayColumn[E any] struct{ name string }

// Array names a Postgres array column, e.g. Array[string]("tags").
func Array[E any](name string) ArrayColumn[E] { return ArrayColumn[E]{name: name} }

func (a ArrayColumn[E]) arrayOp(op string, elems []E) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			if len(elems) == 0 {
				// An untyped empty ARRAY[] is a Postgres error; render the
				// operator's empty-set semantics as a constant instead.
				switch op {
				case "@>":
					return "1=1", nil // every array contains the empty set
				case "&&":
					return "1=0", nil // nothing overlaps the empty set
				default: // "<@": col ⊆ ∅ iff col is empty
					return "cardinality(" + quoteCol(d, a.name) + ") = 0", nil
				}
			}
			var b strings.Builder
			b.WriteString(quoteCol(d, a.name))
			b.WriteByte(' ')
			b.WriteString(op)
			b.WriteString(" ARRAY[")
			args := make([]any, len(elems))
			for i, e := range elems {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteByte('?')
				args[i] = e
			}
			b.WriteByte(']')
			return b.String(), args
		},
		cols: []string{a.name},
		feat: dialect.FeatArray,
	}
}

// Contains tests that the array column contains all of elems (col @> ARRAY[...]).
func (a ArrayColumn[E]) Contains(elems ...E) Predicate { return a.arrayOp("@>", elems) }

// ContainedBy tests that every element of the column is in elems (col <@ ARRAY[...]).
func (a ArrayColumn[E]) ContainedBy(elems ...E) Predicate { return a.arrayOp("<@", elems) }

// Overlaps tests that the column and elems share at least one element (col && ARRAY[...]).
func (a ArrayColumn[E]) Overlaps(elems ...E) Predicate { return a.arrayOp("&&", elems) }

// Has tests element membership: v = ANY(col).
func (a ArrayColumn[E]) Has(v E) Predicate {
	return Predicate{
		render: func(d dialect.Dialect) (string, []any) {
			return "? = ANY(" + quoteCol(d, a.name) + ")", []any{v}
		},
		cols: []string{a.name},
		feat: dialect.FeatArray,
	}
}
