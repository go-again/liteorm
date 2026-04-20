package scan

import "reflect"

// GoTypeName maps a struct field's reflect.Type to a canonical Go type name,
// collapsing the database/sql.Null* wrappers and []byte. Shared by the orm
// schema builder and the gen codegen so the two agree on type names.
func GoTypeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.String() {
	case "time.Time", "sql.NullTime":
		return "time.Time"
	case "sql.NullString":
		return "string"
	case "sql.NullInt64", "sql.NullInt32", "sql.NullInt16":
		return "int64"
	case "sql.NullBool":
		return "bool"
	case "sql.NullFloat64":
		return "float64"
	}
	if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
		return "[]byte"
	}
	return t.Kind().String()
}

// TableNameOf returns t's TableName() method result if it has one (value or
// pointer receiver), else the snake_case of the type name.
func TableNameOf(t reflect.Type) string {
	if tn, ok := reflect.New(t).Interface().(interface{ TableName() string }); ok {
		return tn.TableName()
	}
	return Snake(t.Name())
}
