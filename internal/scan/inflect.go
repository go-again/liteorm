package scan

import (
	"strings"
	"sync"
	"sync/atomic"
)

// pluralTables, when set, makes TableNameOf pluralize the default (no-TableName)
// table name. Off by default; toggled via orm.UsePluralTableNames.
var pluralTables atomic.Bool

// irregularPlurals overrides the rule-based pluralizer for words English doesn't
// inflect regularly. Seeded with common ones; extended via RegisterPlural.
var irregularPlurals sync.Map // snake-case singular word -> plural

// SetPluralTableNames toggles the pluralized default-table-name convention. This
// is the internal switch; the user-facing entry point is orm.UsePluralTableNames.
func SetPluralTableNames(on bool) { pluralTables.Store(on) }

// RegisterPlural registers an irregular plural for a single snake-case word, e.g.
// RegisterPlural("person", "people") — for names the rule-based pluralizer gets
// wrong. The user-facing entry point is orm.RegisterPlural.
func RegisterPlural(singular, plural string) { irregularPlurals.Store(singular, plural) }

// uncountable words pluralize to themselves.
var uncountable = map[string]bool{
	"equipment": true, "information": true, "rice": true, "money": true, "news": true,
	"species": true, "series": true, "fish": true, "sheep": true, "deer": true,
}

func init() {
	for s, p := range map[string]string{
		"person": "people", "child": "children", "man": "men", "woman": "women",
		"tooth": "teeth", "foot": "feet", "mouse": "mice", "goose": "geese",
	} {
		irregularPlurals.Store(s, p)
	}
}

// pluralizeTable pluralizes a snake_case table name by inflecting its last
// underscore segment: user -> users, category -> categories, user_profile ->
// user_profiles. It covers standard English rules plus a small set of irregulars;
// register exceptions with RegisterPlural.
func pluralizeTable(snake string) string {
	head, word := "", snake
	if i := strings.LastIndexByte(snake, '_'); i >= 0 {
		head, word = snake[:i+1], snake[i+1:]
	}
	return head + pluralizeWord(word)
}

func pluralizeWord(w string) string {
	if w == "" {
		return w
	}
	if p, ok := irregularPlurals.Load(w); ok {
		return p.(string)
	}
	if uncountable[w] {
		return w
	}
	switch {
	case hasAnySuffix(w, "s", "x", "z", "ch", "sh"):
		return w + "es" // bus->buses, box->boxes, dish->dishes
	case strings.HasSuffix(w, "y") && len(w) >= 2 && !isVowel(w[len(w)-2]):
		return w[:len(w)-1] + "ies" // category->categories (but day->days)
	default:
		return w + "s"
	}
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, x := range suffixes {
		if strings.HasSuffix(s, x) {
			return true
		}
	}
	return false
}

func isVowel(b byte) bool {
	switch b {
	case 'a', 'e', 'i', 'o', 'u':
		return true
	}
	return false
}
