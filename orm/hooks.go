package orm

import (
	"context"
	"reflect"
	"sync"

	liteorm "liteorm.org"
)

// Op is the narrow, explicit handle passed to a hook — the executing session and
// the typed model. It is not a mutable shared *DB: control flow and capabilities
// are visible at the signature, and hook errors propagate and abort (they are
// never swallowed). Wrong hook signatures are a compile error, not a
// silently-dead hook, because the interfaces are typed on T.
type Op[T any] struct {
	Sess  liteorm.Session
	Model *T
}

// Hook interfaces. A model opts in by implementing the ones it needs on *T:
//
//	func (u *User) BeforeCreate(ctx context.Context, op *orm.Op[User]) error { ... }
type (
	BeforeCreateHook[T any] interface {
		BeforeCreate(ctx context.Context, op *Op[T]) error
	}
	AfterCreateHook[T any] interface {
		AfterCreate(ctx context.Context, op *Op[T]) error
	}
	BeforeUpdateHook[T any] interface {
		BeforeUpdate(ctx context.Context, op *Op[T]) error
	}
	AfterUpdateHook[T any] interface {
		AfterUpdate(ctx context.Context, op *Op[T]) error
	}
	BeforeDeleteHook[T any] interface {
		BeforeDelete(ctx context.Context, op *Op[T]) error
	}
	AfterDeleteHook[T any] interface {
		AfterDelete(ctx context.Context, op *Op[T]) error
	}
)

type hookFlags struct {
	beforeCreate, afterCreate, beforeUpdate, afterUpdate, beforeDelete, afterDelete bool
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
	}
	actual, _ := hookCache.LoadOrStore(t, h)
	return actual.(hookFlags)
}

func impl[I any](z any) bool { _, ok := z.(I); return ok }

func fireBeforeCreate[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().beforeCreate {
		return nil
	}
	return any(op.Model).(BeforeCreateHook[T]).BeforeCreate(ctx, op)
}
func fireAfterCreate[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().afterCreate {
		return nil
	}
	return any(op.Model).(AfterCreateHook[T]).AfterCreate(ctx, op)
}
func fireBeforeUpdate[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().beforeUpdate {
		return nil
	}
	return any(op.Model).(BeforeUpdateHook[T]).BeforeUpdate(ctx, op)
}
func fireAfterUpdate[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().afterUpdate {
		return nil
	}
	return any(op.Model).(AfterUpdateHook[T]).AfterUpdate(ctx, op)
}
func fireBeforeDelete[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().beforeDelete {
		return nil
	}
	return any(op.Model).(BeforeDeleteHook[T]).BeforeDelete(ctx, op)
}
func fireAfterDelete[T any](ctx context.Context, op *Op[T]) error {
	if !hooksFor[T]().afterDelete {
		return nil
	}
	return any(op.Model).(AfterDeleteHook[T]).AfterDelete(ctx, op)
}
