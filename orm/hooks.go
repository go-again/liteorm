package orm

import (
	"context"
	"reflect"
	"sync"

	liteorm "liteorm.org"
)

// Event is the narrow, explicit handle passed to a hook — the executing session and
// the typed model. It is not a mutable shared *DB: control flow and capabilities
// are visible at the signature, and hook errors propagate and abort (they are
// never swallowed). Wrong hook signatures are a compile error, not a
// silently-dead hook, because the interfaces are typed on T.
type Event[T any] struct {
	Sess  liteorm.Session
	Model *T
	// Columns is the set of columns the write touches — the resolved write set
	// after any Select/Omit. It is populated on the create and update paths
	// (Create, Update, Upsert, CreateInBatches, and Restore's soft-delete column)
	// so a hook can see what is being written, and is nil on read and delete
	// hooks. It is read-only advisory: mutating it does not change which columns
	// are written.
	Columns []string
}

// Hook interfaces. A model opts in by implementing the ones it needs on *T:
//
//	func (u *User) BeforeCreate(ctx context.Context, ev *orm.Event[User]) error { ... }
//
// BeforeSave/AfterSave fire on both Create and Update (around the more specific
// Before/AfterCreate / Before/AfterUpdate); AfterFind fires after a read
// hydrates a row, on the orm repository read paths.
type (
	BeforeCreateHook[T any] interface {
		BeforeCreate(ctx context.Context, ev *Event[T]) error
	}
	AfterCreateHook[T any] interface {
		AfterCreate(ctx context.Context, ev *Event[T]) error
	}
	BeforeUpdateHook[T any] interface {
		BeforeUpdate(ctx context.Context, ev *Event[T]) error
	}
	AfterUpdateHook[T any] interface {
		AfterUpdate(ctx context.Context, ev *Event[T]) error
	}
	BeforeDeleteHook[T any] interface {
		BeforeDelete(ctx context.Context, ev *Event[T]) error
	}
	AfterDeleteHook[T any] interface {
		AfterDelete(ctx context.Context, ev *Event[T]) error
	}
	BeforeSaveHook[T any] interface {
		BeforeSave(ctx context.Context, ev *Event[T]) error
	}
	AfterSaveHook[T any] interface {
		AfterSave(ctx context.Context, ev *Event[T]) error
	}
	// AfterFindHook fires after a read hydrates a row. It must not issue a read
	// of the same type T through ev.Sess (e.g. orm.NewRepo[T](ev.Sess).Get) —
	// that re-enters AfterFind and recurses without bound.
	AfterFindHook[T any] interface {
		AfterFind(ctx context.Context, ev *Event[T]) error
	}
)

type hookFlags struct {
	beforeCreate, afterCreate, beforeUpdate, afterUpdate, beforeDelete, afterDelete bool
	beforeSave, afterSave, afterFind                                                bool
}

var hookCache sync.Map // reflect.Type -> hookFlags

// hooksFor computes (once per T) which hook interfaces *T implements, so dispatch
// is a cached lookup plus a single typed assert only when a hook is present — no
// reflection, no signature-string compare, no repeated failed asserts.
func hooksFor[T any]() hookFlags {
	t := reflect.TypeFor[T]()
	if h, ok := hookCache.Load(t); ok {
		return h.(hookFlags)
	}
	var z *T
	h := hookFlags{
		beforeCreate: impl[BeforeCreateHook[T]](z),
		afterCreate:  impl[AfterCreateHook[T]](z),
		beforeUpdate: impl[BeforeUpdateHook[T]](z),
		afterUpdate:  impl[AfterUpdateHook[T]](z),
		beforeDelete: impl[BeforeDeleteHook[T]](z),
		afterDelete:  impl[AfterDeleteHook[T]](z),
		beforeSave:   impl[BeforeSaveHook[T]](z),
		afterSave:    impl[AfterSaveHook[T]](z),
		afterFind:    impl[AfterFindHook[T]](z),
	}
	actual, _ := hookCache.LoadOrStore(t, h)
	return actual.(hookFlags)
}

func impl[I any](z any) bool { _, ok := z.(I); return ok }

func fireBeforeCreate[T any](ctx context.Context, ev *Event[T]) error {
	h := hooksFor[T]()
	if h.beforeSave {
		if err := any(ev.Model).(BeforeSaveHook[T]).BeforeSave(ctx, ev); err != nil {
			return err
		}
	}
	if h.beforeCreate {
		return any(ev.Model).(BeforeCreateHook[T]).BeforeCreate(ctx, ev)
	}
	return nil
}
func fireAfterCreate[T any](ctx context.Context, ev *Event[T]) error {
	h := hooksFor[T]()
	if h.afterCreate {
		if err := any(ev.Model).(AfterCreateHook[T]).AfterCreate(ctx, ev); err != nil {
			return err
		}
	}
	if h.afterSave {
		if err := any(ev.Model).(AfterSaveHook[T]).AfterSave(ctx, ev); err != nil {
			return err
		}
	}
	return syncSearchUpsert(ctx, ev)
}
func fireBeforeUpdate[T any](ctx context.Context, ev *Event[T]) error {
	h := hooksFor[T]()
	if h.beforeSave {
		if err := any(ev.Model).(BeforeSaveHook[T]).BeforeSave(ctx, ev); err != nil {
			return err
		}
	}
	if h.beforeUpdate {
		return any(ev.Model).(BeforeUpdateHook[T]).BeforeUpdate(ctx, ev)
	}
	return nil
}
func fireAfterUpdate[T any](ctx context.Context, ev *Event[T]) error {
	h := hooksFor[T]()
	if h.afterUpdate {
		if err := any(ev.Model).(AfterUpdateHook[T]).AfterUpdate(ctx, ev); err != nil {
			return err
		}
	}
	if h.afterSave {
		if err := any(ev.Model).(AfterSaveHook[T]).AfterSave(ctx, ev); err != nil {
			return err
		}
	}
	return syncSearchUpsert(ctx, ev)
}

// fireAfterFindPtr runs the AfterFind hook on a single hydrated row, if T has one.
func fireAfterFindPtr[T any](ctx context.Context, sess liteorm.Session, v *T) error {
	if !hooksFor[T]().afterFind {
		return nil
	}
	return any(v).(AfterFindHook[T]).AfterFind(ctx, &Event[T]{Sess: sess, Model: v})
}

// fireAfterFind runs the AfterFind hook on each hydrated row, if T has one. It is
// a no-op (no per-row loop) for a model without the hook.
func fireAfterFind[T any](ctx context.Context, sess liteorm.Session, rows []T) error {
	if !hooksFor[T]().afterFind {
		return nil
	}
	for i := range rows {
		if err := fireAfterFindPtr(ctx, sess, &rows[i]); err != nil {
			return err
		}
	}
	return nil
}
func fireBeforeDelete[T any](ctx context.Context, ev *Event[T]) error {
	if !hooksFor[T]().beforeDelete {
		return nil
	}
	return any(ev.Model).(BeforeDeleteHook[T]).BeforeDelete(ctx, ev)
}
func fireAfterDelete[T any](ctx context.Context, ev *Event[T]) error {
	if !hooksFor[T]().afterDelete {
		return nil
	}
	return any(ev.Model).(AfterDeleteHook[T]).AfterDelete(ctx, ev)
}
