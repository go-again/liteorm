package orm_test

import (
	"testing"

	"liteorm.org/orm"
)

type plUser struct{ ID int64 }
type plCategory struct{ ID int64 }
type plBox struct{ ID int64 }
type plUserProfile struct{ ID int64 }
type plPerson struct{ ID int64 }
type plQuiz struct{ ID int64 }

type plExplicit struct{ ID int64 }

func (plExplicit) TableName() string { return "my_explicit" }

func tableOf[T any](t *testing.T) string {
	t.Helper()
	s, err := orm.SchemaOf[T]()
	if err != nil {
		t.Fatal(err)
	}
	return s.Table
}

func TestUsePluralTableNames(t *testing.T) {
	// Default (off): exact snake_case singular.
	if got := tableOf[plUser](t); got != "pl_user" {
		t.Fatalf("default table name = %q, want pl_user", got)
	}

	orm.UsePluralTableNames(true)
	t.Cleanup(func() { orm.UsePluralTableNames(false) })
	// The default z->es rule gives the wrong plural for "quiz"; register the real
	// one. (Keyed on the bare last segment, so it applies to the pl_quiz table.)
	orm.RegisterPlural("quiz", "quizzes")

	for _, tc := range []struct{ got, want string }{
		{tableOf[plUser](t), "pl_users"},
		{tableOf[plCategory](t), "pl_categories"},
		{tableOf[plBox](t), "pl_boxes"},
		{tableOf[plUserProfile](t), "pl_user_profiles"},
		{tableOf[plPerson](t), "pl_people"},     // built-in irregular (person -> people)
		{tableOf[plQuiz](t), "pl_quizzes"},      // RegisterPlural override of the rule
		{tableOf[plExplicit](t), "my_explicit"}, // explicit TableName always wins
	} {
		if tc.got != tc.want {
			t.Errorf("table name = %q, want %q", tc.got, tc.want)
		}
	}

	// Toggling back restores the singular default (the cache was reset).
	orm.UsePluralTableNames(false)
	if got := tableOf[plUser](t); got != "pl_user" {
		t.Errorf("after toggle off, table name = %q, want pl_user", got)
	}
}
