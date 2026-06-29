package lob

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"reflect"

	gosqlite "gosqlite.org"
	"gosqlite.org/blobstore"
	liteorm "liteorm.org"
	"liteorm.org/dialect/sqlite"
	"liteorm.org/query"
)

// TxWriter is an io.WriterAt+io.Closer over a large object's content written
// inside an InTx transaction. Close is a no-op — InTx owns the transaction.
type TxWriter interface {
	io.WriterAt
	io.Closer
}

// Tx is the handle passed to InTx's callback: a transaction on a single pinned
// connection that carries both the ORM session (Session) and the large-object
// content writes, so they commit or roll back together.
type Tx struct {
	sess *liteorm.DB
	g    *gosqlite.DB
	sc   *sql.Conn
}

// Session returns the transaction-bound session for ORM / query work — e.g.
// orm.NewRepo(tx.Session()) or query.Select[T](tx.Session()). Its writes run on
// the same connection and transaction as the content written via OpenTx / DropTx.
func (tx *Tx) Session() liteorm.Session { return tx.sess }

// InTx runs fn inside one transaction on a single pinned connection from sess's
// pool. The ORM work done via tx.Session() and the large-object content written
// via OpenTx / DropTx all run on that connection, so a returned error (or panic)
// rolls back the row changes AND the content together, and a nil return commits
// both. This closes the gap that plain lob.Open content writes are not part of an
// ORM transaction.
//
// Run AutoMigrate before the first InTx (so the content store is already
// provisioned) and keep the pool at more than one connection (the default), since
// the transaction holds one connection for its duration.
func InTx(ctx context.Context, sess liteorm.Session, fn func(tx *Tx) error) error {
	g, ok := sqlite.Conn(sess)
	if !ok {
		return ErrUnsupportedBackend
	}
	bound, sc, release, err := sqlite.PinConn(ctx, sess)
	if err != nil {
		return err
	}
	defer func() { _ = release() }()
	if _, err := sc.ExecContext(ctx, "BEGIN"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = sc.ExecContext(ctx, "ROLLBACK")
		}
	}()
	if err := fn(&Tx{sess: bound, g: g, sc: sc}); err != nil {
		return err
	}
	if _, err := sc.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// OpenTx returns an io.WriterAt over the row's content, allocating the backing
// object (and writing its id back into the row) on first use — all inside the
// InTx transaction, so the content and the row id commit or roll back together.
// A WithCompression option sets the object's at-rest compression.
func OpenTx[T any](ctx context.Context, tx *Tx, row *T, field string, opts ...Option) (TxWriter, error) {
	b, err := bind[T](field)
	if err != nil {
		return nil, err
	}
	st, err := storeForField(tx.g, b)
	if err != nil {
		return nil, err
	}
	cs := st.OnConn(tx.sc)
	id, err := ensure[T](ctx, tx.sess, cs, reflect.ValueOf(row).Elem(), b, collect(opts).createOpts()...)
	if err != nil {
		return nil, err
	}
	return &connWriter{ctx: ctx, cs: cs, id: id}, nil
}

// DropTx frees the row's content object and clears the field inside the InTx
// transaction, so a row and its content can be deleted atomically. No-op for a
// field that was never written.
func DropTx[T any](ctx context.Context, tx *Tx, row *T, field string) error {
	b, err := bind[T](field)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(row).Elem()
	fv := rv.FieldByIndex(b.lf.Index)
	id := fv.Int()
	if id == 0 {
		return nil
	}
	st, err := storeForField(tx.g, b)
	if err != nil {
		return err
	}
	cs := st.OnConn(tx.sc)
	if err := cs.Delete(ctx, id); err != nil && !errors.Is(err, blobstore.ErrNotFound) {
		return err
	}
	pkv := rv.FieldByIndex(b.pkIndex).Interface()
	pkRef := string(tx.sess.Dialect().QuoteIdent(nil, b.pkCol))
	if _, err := query.Update[T](tx.sess).Set(b.lf.Column, 0).Where(pkRef+" = ?", pkv).Exec(ctx); err != nil {
		return fmt.Errorf("lob: clear object id: %w", err)
	}
	fv.SetInt(0)
	return nil
}

// connWriter adapts a *blobstore.ConnStore to io.WriterAt for a single object.
type connWriter struct {
	ctx context.Context
	cs  *blobstore.ConnStore
	id  int64
}

func (w *connWriter) WriteAt(p []byte, off int64) (int, error) {
	return w.cs.WriteAt(w.ctx, w.id, p, off)
}
func (w *connWriter) Close() error { return nil }
