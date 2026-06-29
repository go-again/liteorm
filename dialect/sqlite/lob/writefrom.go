package lob

import (
	"context"
	"io"
	"reflect"

	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
)

// WriteFrom streams r into the row's content starting at offset 0, allocating the
// backing object (and writing its id back into the row) on first use, and returns
// the number of bytes written. It writes in one engine transaction. It does not
// truncate first, so writing fewer bytes than the object already holds leaves the
// trailing bytes — Truncate to the written length if you need an exact replace. A
// WithCompression / WithVersioning option configures the object on first creation.
func WriteFrom[T any](ctx context.Context, sess liteorm.Session, row *T, field string, r io.Reader, opts ...Option) (int64, error) {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return 0, ErrUnsupportedBackend
	}
	b, err := bind[T](field)
	if err != nil {
		return 0, err
	}
	st, err := storeForField(g, b)
	if err != nil {
		return 0, err
	}
	id, err := ensure[T](ctx, sess, st, reflect.ValueOf(row).Elem(), b, collect(opts).createOpts()...)
	if err != nil {
		return 0, err
	}
	return st.WriteAtFrom(ctx, id, 0, r)
}

// WriteFromTx is WriteFrom inside an InTx transaction: the content stream and the
// row's id-writeback commit or roll back with the rest of the transaction.
func WriteFromTx[T any](ctx context.Context, tx *Tx, row *T, field string, r io.Reader, opts ...Option) (int64, error) {
	b, err := bind[T](field)
	if err != nil {
		return 0, err
	}
	st, err := storeForField(tx.g, b)
	if err != nil {
		return 0, err
	}
	cs := st.OnConn(tx.sc)
	id, err := ensure[T](ctx, tx.sess, cs, reflect.ValueOf(row).Elem(), b, collect(opts).createOpts()...)
	if err != nil {
		return 0, err
	}
	return cs.WriteAtFrom(ctx, id, 0, r)
}
