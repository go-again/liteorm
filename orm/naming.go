package orm

import "liteorm.org/internal/scan"

// UsePluralTableNames switches the default table-name convention to pluralized
// snake_case — User -> "users", Category -> "categories", UserProfile ->
// "user_profiles" — matching gorm's default, to ease porting. It is off by
// default: liteorm otherwise uses the exact snake_case of the type name. A model's
// own TableName() method always wins, regardless of this setting.
//
// It applies to both the orm and query front-ends (they share the naming
// resolver). Call it once at startup, before any schema is resolved; it resets
// the orm schema cache so a toggle takes effect, but other components may have
// already captured a name. Register irregular plurals the rules miss with
// RegisterPlural.
func UsePluralTableNames(on bool) {
	scan.SetPluralTableNames(on)
	schemaCache.Clear()
}

// RegisterPlural overrides the pluralization of a single irregular word for the
// pluralized table-name convention — e.g. RegisterPlural("quiz", "quizzes"). Only
// the last snake_case segment of a table name is inflected, so register the bare
// word, not a compound (register "person", which also covers "blog_person").
// Common irregulars (person, child, man, …) are built in; use this for the rest,
// or for domain words the rule-based pluralizer gets wrong.
func RegisterPlural(singular, plural string) { scan.RegisterPlural(singular, plural) }
