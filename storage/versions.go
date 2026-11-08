package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Versions: every entry has a draft row per locale, which saves edit, and
// at most one published row, a copy of the draft made by Publish. Both
// share the document ID and own separate components.

func (s *SQLStore) SaveDraft(ctx context.Context, e *Entry) error {
	t, err := s.entryTable(e.Type)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q querier) error {
		e.Version = Draft
		if e.ID == 0 {
			e.CreatedAt = s.now()
			return s.insertRow(ctx, q, t, e, 1)
		}
		rowID, rev, err := s.row(ctx, q, t, e.ID, e.Locale, Draft)
		if errors.Is(err, ErrNotFound) {
			// A new locale of an existing document.
			created, err := s.documentCreated(ctx, q, t, e.ID)
			if err != nil {
				return err
			}
			e.CreatedAt = created
			return s.insertRow(ctx, q, t, e, 1)
		}
		if err != nil {
			return err
		}
		return s.updateRow(ctx, q, t, rowID, e, rev+1)
	})
}

func (s *SQLStore) Publish(ctx context.Context, typ string, id int64, locale string) error {
	t, err := s.entryTable(typ)
	if err != nil {
		return err
	}
	return s.inView(ctx, func(v *SQLStore, q querier) error {
		_, rev, err := v.row(ctx, q, t, id, locale, Draft)
		if err != nil {
			return err
		}
		draft, err := v.GetEntry(ctx, typ, id, locale, Draft)
		if err != nil {
			return err
		}
		// The draft's item keys name draft components, which the published
		// row never reuses: it gets its own copies.
		pub := *draft
		pub.Version = Published
		rowID, _, err := v.row(ctx, q, t, id, locale, Published)
		if errors.Is(err, ErrNotFound) {
			return v.insertRow(ctx, q, t, &pub, rev)
		}
		if err != nil {
			return err
		}
		return v.updateRow(ctx, q, t, rowID, &pub, rev)
	})
}

func (s *SQLStore) Unpublish(ctx context.Context, typ string, id int64, locale string) error {
	t, err := s.entryTable(typ)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q querier) error {
		rowID, _, err := s.row(ctx, q, t, id, locale, Published)
		if err != nil {
			return err
		}
		return s.deleteRow(ctx, q, t, rowID)
	})
}

// row returns the row ID and revision of one version of a document.
func (s *SQLStore) row(ctx context.Context, q querier, t *table, id int64, locale string, v Version) (rowID, rev int64, err error) {
	err = q.QueryRowContext(ctx, `SELECT id, revision FROM `+t.Name+` WHERE document_id = ? AND locale = ? AND version = ?`, id, locale, v).Scan(&rowID, &rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, ErrNotFound
	}
	return rowID, rev, err
}

// documentCreated returns when a document was created (its oldest row).
func (s *SQLStore) documentCreated(ctx context.Context, q querier, t *table, id int64) (time.Time, error) {
	var created sql.NullInt64
	if err := q.QueryRowContext(ctx, `SELECT MIN(created_at) FROM `+t.Name+` WHERE document_id = ?`, id).Scan(&created); err != nil {
		return time.Time{}, err
	}
	if !created.Valid {
		return time.Time{}, ErrNotFound
	}
	return time.Unix(created.Int64, 0), nil
}

// publication sets PublishedAt and Changed. A published row was published
// at its own update time. A draft takes the update time of the published
// row of its document and locale, and is changed unless that row was
// published from the draft's current revision.
func (s *SQLStore) publication(ctx context.Context, t *table, list []Entry, revs []int64) error {
	type key struct {
		doc    int64
		locale string
	}
	type pub struct {
		at  time.Time
		rev int64
	}
	var docs []any
	for i := range list {
		if list[i].Version == Published {
			list[i].PublishedAt = list[i].UpdatedAt
		} else {
			docs = append(docs, list[i].ID)
		}
	}
	if len(docs) == 0 {
		return nil
	}
	pubs := map[key]pub{}
	err := s.inChunks(docs, func(chunk []any) error {
		rows, err := s.conn().QueryContext(ctx, `SELECT document_id, locale, revision, updated_at FROM `+t.Name+` WHERE version = ? AND document_id IN (`+placeholders(len(chunk))+`)`,
			append([]any{Published}, chunk...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k key
			var rev, updated int64
			if err := rows.Scan(&k.doc, &k.locale, &rev, &updated); err != nil {
				return err
			}
			pubs[k] = pub{time.Unix(updated, 0), rev}
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].Version == Draft {
			p, ok := pubs[key{list[i].ID, list[i].Locale}]
			list[i].PublishedAt = p.at
			list[i].Changed = !ok || p.rev != revs[i]
		}
	}
	return nil
}
