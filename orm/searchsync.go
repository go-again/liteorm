package orm

import (
	"context"
	"reflect"

	liteorm "liteorm.org"
	"liteorm.org/dialect"
)

// syncSearchUpsert keeps hook-synced vector sidecars current after an ORM create
// or update, upserting the model's embedding into each hook-mode vector sidecar.
// It is a no-op when the model declares no search indexes, the backend cannot
// sync rows, the key is composite, or the embedding is empty (a partial write
// that did not set it) — matching the "skip empty embeddings" behavior so a
// keyless update never clobbers the sidecar with a zero vector.
func syncSearchUpsert[T any](ctx context.Context, ev *Event[T]) error {
	s, err := SchemaOf[T]()
	if err != nil || len(s.SearchIndexes) == 0 || s.PK == nil {
		return err
	}
	rs, ok := ev.Sess.Dialect().(dialect.SearchRowSyncer)
	if !ok {
		return nil
	}
	mv := reflect.ValueOf(ev.Model).Elem()
	for _, ix := range s.SearchIndexes {
		spec, err := searchSpecOf(s, ix)
		if err != nil {
			return err
		}
		if spec.Kind != dialect.SearchVector || spec.Sync != "hooks" {
			continue // trigger-synced (or non-vector) indexes maintain themselves
		}
		emb := mv.FieldByName(ix.Fields[0])
		if !emb.IsValid() || emb.Kind() != reflect.Slice || emb.Len() == 0 {
			continue
		}
		key := mv.FieldByIndex(s.PK.Index).Interface()
		stmts, err := rs.UpsertSearchRowSQL(spec, key, emb.Interface())
		if err != nil {
			return err
		}
		if err := execSearchStmts(ctx, ev.Sess, stmts); err != nil {
			return err
		}
	}
	return nil
}

// syncSearchDelete removes a row from its hook-mode vector sidecars on a hard
// delete (a soft delete leaves the sidecar row in place; the search helpers
// exclude soft-deleted rows at query time).
func syncSearchDelete[T any](ctx context.Context, sess liteorm.Session, s *Schema, v *T) error {
	if len(s.SearchIndexes) == 0 || s.PK == nil {
		return nil
	}
	rs, ok := sess.Dialect().(dialect.SearchRowSyncer)
	if !ok {
		return nil
	}
	key := reflect.ValueOf(v).Elem().FieldByIndex(s.PK.Index).Interface()
	for _, ix := range s.SearchIndexes {
		spec, err := searchSpecOf(s, ix)
		if err != nil {
			return err
		}
		if spec.Kind != dialect.SearchVector || spec.Sync != "hooks" {
			continue
		}
		stmts, err := rs.DeleteSearchRowSQL(spec, key)
		if err != nil {
			return err
		}
		if err := execSearchStmts(ctx, sess, stmts); err != nil {
			return err
		}
	}
	return nil
}

func execSearchStmts(ctx context.Context, sess liteorm.Session, stmts []dialect.SearchStmt) error {
	for _, st := range stmts {
		if _, err := sess.ExecContext(ctx, st.SQL, st.Args...); err != nil {
			return err
		}
	}
	return nil
}
