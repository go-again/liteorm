package query

import (
	"fmt"

	"liteorm.org/dialect"
	"liteorm.org/internal/sqlgen"
)

// Intersect appends other as an INTERSECT arm (rows in both; duplicates removed).
// IntersectAll keeps duplicates. Like Union, other must produce the same column
// shape; the receiver's ORDER BY/LIMIT apply to the whole compound. INTERSECT is
// gated by FeatIntersectExcept (unsupported on MySQL — a clear build error there).
func (b *SelectBuilder[T]) Intersect(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.compound(other, "INTERSECT", false)
}
func (b *SelectBuilder[T]) IntersectAll(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.compound(other, "INTERSECT", true)
}

// Except appends other as an EXCEPT arm (rows in the receiver but not other).
// ExceptAll keeps duplicates. Gated by FeatIntersectExcept.
func (b *SelectBuilder[T]) Except(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.compound(other, "EXCEPT", false)
}
func (b *SelectBuilder[T]) ExceptAll(other *SelectBuilder[T]) *SelectBuilder[T] {
	return b.compound(other, "EXCEPT", true)
}

func (b *SelectBuilder[T]) compound(other *SelectBuilder[T], op string, all bool) *SelectBuilder[T] {
	sel, err := other.resolved()
	if err != nil {
		if b.buildErr == nil {
			b.buildErr = err
		}
		return b
	}
	if sel.Lock != nil && b.buildErr == nil {
		// A compound arm's ORDER BY/LIMIT/locking don't render (only the receiver's
		// tail applies to the whole compound); reject a per-arm lock rather than
		// silently drop it.
		b.buildErr = fmt.Errorf("query: a row lock cannot be set on a %s arm — apply it to the whole compound", op)
		return b
	}
	if op != "UNION" {
		b.requiredFeat |= dialect.FeatIntersectExcept
	}
	b.requiredFeat |= other.requiredFeat
	b.sel.Union = append(b.sel.Union, sqlgen.CompoundTerm{Op: op, All: all, Select: sel})
	return b
}

// DistinctOn adds SELECT DISTINCT ON (cols) — keep the first row of each distinct
// combination of the given typed columns (validated + quoted). Postgres-only
// (gated by FeatDistinctOn); pair it with an Order whose leading terms match.
func (b *SelectBuilder[T]) DistinctOn(cols ...Field) *SelectBuilder[T] {
	b.distinctOn = append(b.distinctOn, cols...)
	b.requiredFeat |= dialect.FeatDistinctOn
	return b
}

// ForUpdate locks the selected rows for update (FOR UPDATE). ForShare takes a
// weaker shared lock (FOR SHARE). Postgres and MySQL only (gated by
// FeatRowLocking — a clear build error on SQLite/MSSQL).
func (b *SelectBuilder[T]) ForUpdate() *SelectBuilder[T] { return b.lock("UPDATE") }
func (b *SelectBuilder[T]) ForShare() *SelectBuilder[T]  { return b.lock("SHARE") }

func (b *SelectBuilder[T]) lock(strength string) *SelectBuilder[T] {
	if b.sel.Lock == nil {
		b.sel.Lock = &sqlgen.Lock{}
	}
	b.sel.Lock.Strength = strength
	b.requiredFeat |= dialect.FeatRowLocking
	return b
}

// SkipLocked makes a locking read skip rows already locked by another transaction
// (instead of blocking); NoWait makes it error immediately instead. Both imply a
// lock, defaulting to FOR UPDATE when neither ForUpdate nor ForShare was called.
func (b *SelectBuilder[T]) SkipLocked() *SelectBuilder[T] {
	b.ensureLock().SkipLocked = true
	return b
}
func (b *SelectBuilder[T]) NoWait() *SelectBuilder[T] {
	b.ensureLock().NoWait = true
	return b
}

func (b *SelectBuilder[T]) ensureLock() *sqlgen.Lock {
	if b.sel.Lock == nil {
		b.lock("UPDATE")
	}
	return b.sel.Lock
}

// gateFeatures rejects a statement whose requested clauses need a feature the
// dialect lacks, before any SQL runs — the loud-at-build-time stance shared by
// every builder (Select, Update, …) and the predicate feature gate.
func gateFeatures(required dialect.Feature, d dialect.Dialect) error {
	for _, f := range []dialect.Feature{
		dialect.FeatRowLocking, dialect.FeatDistinctOn, dialect.FeatIntersectExcept,
		dialect.FeatCTE, dialect.FeatLateral, dialect.FeatUpdateFrom,
	} {
		if required&f != 0 && !d.Features().Has(f) {
			return fmt.Errorf("query: %s is not supported by the %s dialect", featLabel(f), d.Name())
		}
	}
	return nil
}

func (b *SelectBuilder[T]) checkFeatures(d dialect.Dialect) error {
	return gateFeatures(b.requiredFeat, d)
}

func featLabel(f dialect.Feature) string {
	switch f {
	case dialect.FeatRowLocking:
		return "row locking (FOR UPDATE/SHARE)"
	case dialect.FeatDistinctOn:
		return "DISTINCT ON"
	case dialect.FeatIntersectExcept:
		return "INTERSECT/EXCEPT"
	case dialect.FeatCTE:
		return "common table expressions (WITH)"
	case dialect.FeatLateral:
		return "LATERAL joins"
	case dialect.FeatUpdateFrom:
		return "UPDATE … FROM"
	}
	return "a SQL feature"
}
