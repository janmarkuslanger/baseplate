package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/janmarkuslanger/baseplate/content"
)

// querier is satisfied by *sql.DB and *sql.Tx. With SQLite's single
// connection, every query inside a transaction must use the transaction,
// and a query must never run while another result set is still open.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func (s *SQLStore) UseSchema(ctx context.Context, sc *content.Schema) error {
	l, err := planLayout(sc)
	if err != nil {
		return err
	}
	if err := s.sync(ctx, l); err != nil {
		return err
	}
	s.layout = l
	return nil
}

// col quotes a column name for the store's dialect.
func (s *SQLStore) col(name string) string { return dialects[s.dialect].quote(name) }

func (s *SQLStore) tx(ctx context.Context, fn func(q querier) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *SQLStore) entryTable(typ string) (*table, error) {
	if s.layout == nil {
		return nil, errors.New("storage: UseSchema was not called")
	}
	return s.layout.entry(typ)
}

// --- write ---

func (s *SQLStore) CreateEntry(ctx context.Context, e *Entry) error {
	t, err := s.entryTable(e.Type)
	if err != nil {
		return err
	}
	if e.Status == "" {
		e.Status = StatusDraft
	}
	now := s.now()
	e.CreatedAt, e.UpdatedAt = now, now
	return s.tx(ctx, func(q querier) error {
		cols := []string{"slug", "status", "locale", "created_at", "updated_at"}
		args := []any{e.Slug, e.Status, e.Locale, now.Unix(), now.Unix()}
		for _, c := range t.Columns {
			cols = append(cols, s.col(c.Name))
			args = append(args, scalarArg(c, e.Data[c.Field.Name]))
		}
		res, err := q.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, t.Name, strings.Join(cols, ", "), placeholders(len(cols))), args...)
		if err != nil {
			return err
		}
		if e.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		return s.writeChildren(ctx, q, t, e.ID, e.Data)
	})
}

func (s *SQLStore) UpdateEntry(ctx context.Context, e *Entry) error {
	t, err := s.entryTable(e.Type)
	if err != nil {
		return err
	}
	e.UpdatedAt = s.now()
	return s.tx(ctx, func(q querier) error {
		sets := []string{"slug = ?", "status = ?", "locale = ?", "updated_at = ?"}
		args := []any{e.Slug, e.Status, e.Locale, e.UpdatedAt.Unix()}
		for _, c := range t.Columns {
			sets = append(sets, s.col(c.Name)+" = ?")
			args = append(args, scalarArg(c, e.Data[c.Field.Name]))
		}
		res, err := q.ExecContext(ctx, fmt.Sprintf(`UPDATE %s SET %s WHERE id = ?`, t.Name, strings.Join(sets, ", ")), append(args, e.ID)...)
		if err != nil {
			return err
		}
		if err := mustAffect(res); err != nil {
			return err
		}
		// Components keep their IDs: items carrying the key of a component
		// of this entry are updated (wherever they moved), others are new,
		// and components no longer present are deleted.
		tree := map[compRef]bool{}
		if err := s.collectTree(ctx, q, t.Name, e.ID, tree); err != nil {
			return err
		}
		if err := s.unlink(ctx, q, t.Name, e.ID, tree); err != nil {
			return err
		}
		r := &reuse{tree: tree, used: map[compRef]bool{}}
		if err := s.writeChildrenReuse(ctx, q, t, e.ID, e.Data, r); err != nil {
			return err
		}
		for c := range tree {
			if !r.used[c] {
				if _, err := q.ExecContext(ctx, `DELETE FROM `+c.table+` WHERE id = ?`, c.id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (s *SQLStore) DeleteEntry(ctx context.Context, typ string, id int64) error {
	t, err := s.entryTable(typ)
	if err != nil {
		return err
	}
	return s.tx(ctx, func(q querier) error {
		res, err := q.ExecContext(ctx, `DELETE FROM `+t.Name+` WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if err := mustAffect(res); err != nil {
			return err
		}
		if err := s.deleteChildren(ctx, q, t.Name, id); err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `DELETE FROM relations WHERE target_type = ? AND target_id = ?`, typ, id)
		return err
	})
}

// itemKeyOf is the item key of a component row: "<table>:<id>".
func itemKeyOf(table string, id int64) string { return table + ":" + strconv.FormatInt(id, 10) }

type compRef struct {
	table string
	id    int64
}

// reuse tracks existing components of an entry during an update.
type reuse struct {
	tree map[compRef]bool
	used map[compRef]bool
}

// take returns the ID of an existing component the item refers to, or 0.
func (r *reuse) take(table string, data map[string]any) int64 {
	if r == nil {
		return 0
	}
	k, _ := data[content.ItemKey].(string)
	tbl, num, ok := strings.Cut(k, ":")
	if !ok || tbl != table {
		return 0
	}
	id, err := strconv.ParseInt(num, 10, 64)
	c := compRef{table, id}
	if err != nil || !r.tree[c] || r.used[c] {
		return 0
	}
	r.used[c] = true
	return id
}

// collectTree adds every component below an owner row to tree.
func (s *SQLStore) collectTree(ctx context.Context, q querier, ownerTable string, ownerID int64, tree map[compRef]bool) error {
	links, err := s.childLinks(ctx, q, ownerTable, ownerID)
	if err != nil {
		return err
	}
	for _, l := range links {
		if _, ok := s.layout.tables[l.table]; !ok {
			continue
		}
		tree[compRef{l.table, l.compID}] = true
		if err := s.collectTree(ctx, q, l.table, l.compID, tree); err != nil {
			return err
		}
	}
	return nil
}

// unlink removes all component links and owned relations of the root row
// and its components, keeping the component rows for reuse.
func (s *SQLStore) unlink(ctx context.Context, q querier, rootTable string, rootID int64, tree map[compRef]bool) error {
	owners := append([]compRef{{rootTable, rootID}}, keys(tree)...)
	for _, o := range owners {
		if _, err := q.ExecContext(ctx, `DELETE FROM components WHERE owner_table = ? AND owner_id = ?`, o.table, o.id); err != nil {
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM relations WHERE owner_table = ? AND owner_id = ?`, o.table, o.id); err != nil {
			return err
		}
	}
	return nil
}

func keys(m map[compRef]bool) []compRef {
	out := make([]compRef, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func (s *SQLStore) childLinks(ctx context.Context, q querier, ownerTable string, ownerID int64) ([]link, error) {
	rows, err := q.QueryContext(ctx, `SELECT component_table, component_id FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	if err != nil {
		return nil, err
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.table, &l.compID); err != nil {
			rows.Close()
			return nil, err
		}
		links = append(links, l)
	}
	rows.Close()
	return links, rows.Err()
}

// writeChildren stores the components and relations of one owner row.
func (s *SQLStore) writeChildren(ctx context.Context, q querier, t *table, ownerID int64, data map[string]any) error {
	return s.writeChildrenReuse(ctx, q, t, ownerID, data, nil)
}

func (s *SQLStore) writeChildrenReuse(ctx context.Context, q querier, t *table, ownerID int64, data map[string]any, r *reuse) error {
	for _, f := range t.Fields {
		v := data[f.Name]
		switch s.layout.fieldStorage(f) {
		case content.StoreGroup:
			if m, ok := v.(map[string]any); ok {
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, t.children[f.Name], 0, m, r); err != nil {
					return err
				}
			}
		case content.StoreList:
			for i, it := range asItems(v) {
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, t.children[f.Name], i, it, r); err != nil {
					return err
				}
			}
		case content.StoreBlocks:
			for i, it := range asItems(v) {
				name, _ := it[content.BlockTypeKey].(string)
				if _, ok := s.layout.tables[blockTable(name)]; !ok {
					return fmt.Errorf("storage: unknown block %q in %s.%s", name, t.Name, f.Name)
				}
				if err := s.writeComponent(ctx, q, t.Name, ownerID, f.Name, blockTable(name), i, it, r); err != nil {
					return err
				}
			}
		case content.StoreRelation:
			for i, target := range relationIDs(v) {
				var one int
				err := q.QueryRowContext(ctx, `SELECT 1 FROM `+entryTable(f.Ref)+` WHERE id = ?`, target).Scan(&one)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w (%s → %s #%d)", ErrInvalidRelation, f.Name, f.Ref, target)
				}
				if err != nil {
					return err
				}
				if _, err := q.ExecContext(ctx, `INSERT INTO relations (owner_table, owner_id, field, target_type, target_id, position) VALUES (?, ?, ?, ?, ?, ?)`,
					t.Name, ownerID, f.Name, f.Ref, target, i); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *SQLStore) writeComponent(ctx context.Context, q querier, ownerTable string, ownerID int64, field, tableName string, pos int, data map[string]any, r *reuse) error {
	ct := s.layout.tables[tableName]
	id := r.take(ct.Name, data)
	cols := make([]string, len(ct.Columns))
	args := make([]any, len(ct.Columns))
	for i, c := range ct.Columns {
		cols[i], args[i] = s.col(c.Name), scalarArg(c, data[c.Field.Name])
	}
	switch {
	case id != 0 && len(cols) > 0:
		sets := make([]string, len(cols))
		for i, c := range cols {
			sets[i] = c + " = ?"
		}
		if _, err := q.ExecContext(ctx, `UPDATE `+ct.Name+` SET `+strings.Join(sets, ", ")+` WHERE id = ?`, append(args, id)...); err != nil {
			return err
		}
	case id != 0:
		// nothing to update
	default:
		var res sql.Result
		var err error
		switch {
		case len(cols) == 0 && s.dialect == MySQL:
			res, err = q.ExecContext(ctx, `INSERT INTO `+ct.Name+` () VALUES ()`)
		case len(cols) == 0:
			res, err = q.ExecContext(ctx, `INSERT INTO `+ct.Name+` DEFAULT VALUES`)
		default:
			res, err = q.ExecContext(ctx, fmt.Sprintf(`INSERT INTO %s (%s) VALUES (%s)`, ct.Name, strings.Join(cols, ", "), placeholders(len(cols))), args...)
		}
		if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
	}
	if _, err := q.ExecContext(ctx, `INSERT INTO components (owner_table, owner_id, field, component_table, component_id, position) VALUES (?, ?, ?, ?, ?, ?)`,
		ownerTable, ownerID, field, ct.Name, id, pos); err != nil {
		return err
	}
	return s.writeChildrenReuse(ctx, q, ct, id, data, r)
}

type link struct {
	ownerID, compID int64
	field, table    string
}

// deleteChildren removes all components (recursively) and relations owned
// by one row.
func (s *SQLStore) deleteChildren(ctx context.Context, q querier, ownerTable string, ownerID int64) error {
	rows, err := q.QueryContext(ctx, `SELECT component_table, component_id FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	if err != nil {
		return err
	}
	var links []link
	for rows.Next() {
		var l link
		if err := rows.Scan(&l.table, &l.compID); err != nil {
			rows.Close()
			return err
		}
		links = append(links, l)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, l := range links {
		if err := s.deleteChildren(ctx, q, l.table, l.compID); err != nil {
			return err
		}
		if _, ok := s.layout.tables[l.table]; ok {
			if _, err := q.ExecContext(ctx, `DELETE FROM `+l.table+` WHERE id = ?`, l.compID); err != nil {
				return err
			}
		}
	}
	if _, err := q.ExecContext(ctx, `DELETE FROM components WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID); err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `DELETE FROM relations WHERE owner_table = ? AND owner_id = ?`, ownerTable, ownerID)
	return err
}

// --- read ---

func (s *SQLStore) GetEntry(ctx context.Context, typ string, id int64) (*Entry, error) {
	return s.getOne(ctx, typ, `id = ?`, id)
}

func (s *SQLStore) GetEntryBySlug(ctx context.Context, typ, slug string) (*Entry, error) {
	return s.getOne(ctx, typ, `slug = ?`, slug)
}

func (s *SQLStore) getOne(ctx context.Context, typ, cond string, arg any) (*Entry, error) {
	t, err := s.entryTable(typ)
	if err != nil {
		return nil, err
	}
	list, err := s.selectEntries(ctx, t, ` WHERE `+cond+` ORDER BY id LIMIT 1`, arg)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (s *SQLStore) ListEntries(ctx context.Context, lq ListQuery) ([]Entry, int, error) {
	t, err := s.entryTable(lq.Type)
	if err != nil {
		return nil, 0, err
	}
	var where []string
	var args []any
	if lq.Status != "" {
		where = append(where, "status = ?")
		args = append(args, lq.Status)
	}
	if lq.Search != "" {
		like := "%" + lq.Search + "%"
		ors := []string{"slug LIKE ?"}
		args = append(args, like)
		for _, c := range t.Columns {
			if c.Storage == content.StoreText || c.Storage == content.StoreLongText {
				ors = append(ors, s.col(c.Name)+" LIKE ?")
				args = append(args, like)
			}
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	cond := ""
	if len(where) > 0 {
		cond = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+t.Name+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	tail := cond + ` ORDER BY updated_at DESC, id DESC`
	if lq.Limit > 0 {
		tail += fmt.Sprintf(" LIMIT %d OFFSET %d", lq.Limit, lq.Offset)
	}
	list, err := s.selectEntries(ctx, t, tail, args...)
	return list, total, err
}

// selectEntries loads entry rows and hydrates their components and
// relations with a fixed number of queries per nesting level.
func (s *SQLStore) selectEntries(ctx context.Context, t *table, tail string, args ...any) ([]Entry, error) {
	cols := []string{"id", "slug", "status", "locale", "created_at", "updated_at"}
	for _, c := range t.Columns {
		cols = append(cols, s.col(c.Name))
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+t.Name+tail, args...)
	if err != nil {
		return nil, err
	}
	var list []Entry
	for rows.Next() {
		var e Entry
		var created, updated int64
		dest := []any{&e.ID, &e.Slug, &e.Status, &e.Locale, &created, &updated}
		vals := scalarDests(t)
		if err := rows.Scan(append(dest, vals...)...); err != nil {
			rows.Close()
			return nil, err
		}
		e.Type = t.Type
		e.CreatedAt, e.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		e.Data = scalarData(t, vals)
		for _, f := range t.Fields {
			if f.Name == "slug" {
				e.Data["slug"] = e.Slug
			}
		}
		list = append(list, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	owners := make(map[int64]map[string]any, len(list))
	for i := range list {
		owners[list[i].ID] = list[i].Data
	}
	if err := s.hydrate(ctx, t, owners); err != nil {
		return nil, err
	}
	return list, nil
}

// hydrate fills container and relation fields of the given rows of t.
func (s *SQLStore) hydrate(ctx context.Context, t *table, owners map[int64]map[string]any) error {
	if len(owners) == 0 {
		return nil
	}
	ids := make([]any, 0, len(owners))
	for id, data := range owners {
		ids = append(ids, id)
		for _, f := range t.Fields {
			switch st := s.layout.fieldStorage(f); {
			case st == content.StoreList || st == content.StoreBlocks || (st == content.StoreRelation && f.Many):
				data[f.Name] = []any{}
			}
		}
	}

	// Components: links first, then one query per component table.
	var links []link
	err := s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT owner_id, field, component_table, component_id FROM components WHERE owner_table = ? AND owner_id IN (`+placeholders(len(chunk))+`) ORDER BY position`,
			append([]any{t.Name}, chunk...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var l link
			if err := rows.Scan(&l.ownerID, &l.field, &l.table, &l.compID); err != nil {
				return err
			}
			links = append(links, l)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	byTable := map[string]map[int64]map[string]any{}
	for _, l := range links {
		if _, ok := s.layout.tables[l.table]; !ok {
			continue // block or field removed from the schema
		}
		if byTable[l.table] == nil {
			byTable[l.table] = map[int64]map[string]any{}
		}
		byTable[l.table][l.compID] = nil
	}
	for name, comps := range byTable {
		ct := s.layout.tables[name]
		if err := s.loadComponents(ctx, ct, comps); err != nil {
			return err
		}
		if err := s.hydrate(ctx, ct, comps); err != nil {
			return err
		}
	}
	for _, l := range links {
		comp := byTable[l.table][l.compID]
		owner := owners[l.ownerID]
		if comp == nil || owner == nil {
			continue
		}
		f, ok := fieldOf(t, l.field)
		if !ok {
			continue
		}
		switch s.layout.fieldStorage(f) {
		case content.StoreGroup:
			comp[content.ItemKey] = itemKeyOf(l.table, l.compID)
			owner[f.Name] = comp
		case content.StoreBlocks:
			comp[content.BlockTypeKey] = blockName(l.table)
			fallthrough
		case content.StoreList:
			comp[content.ItemKey] = itemKeyOf(l.table, l.compID)
			owner[f.Name] = append(owner[f.Name].([]any), comp)
		}
	}

	// Relations.
	return s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT owner_id, field, target_id FROM relations WHERE owner_table = ? AND owner_id IN (`+placeholders(len(chunk))+`) ORDER BY position`,
			append([]any{t.Name}, chunk...)...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var ownerID, target int64
			var field string
			if err := rows.Scan(&ownerID, &field, &target); err != nil {
				return err
			}
			f, ok := fieldOf(t, field)
			if !ok || owners[ownerID] == nil {
				continue
			}
			if f.Many {
				owners[ownerID][field] = append(owners[ownerID][field].([]any), target)
			} else {
				owners[ownerID][field] = target
			}
		}
		return rows.Err()
	})
}

// loadComponents reads the scalar columns of the given component rows.
func (s *SQLStore) loadComponents(ctx context.Context, ct *table, comps map[int64]map[string]any) error {
	ids := make([]any, 0, len(comps))
	for id := range comps {
		ids = append(ids, id)
		comps[id] = map[string]any{}
	}
	cols := []string{"id"}
	for _, c := range ct.Columns {
		cols = append(cols, s.col(c.Name))
	}
	return s.inChunks(ids, func(chunk []any) error {
		rows, err := s.db.QueryContext(ctx, `SELECT `+strings.Join(cols, ", ")+` FROM `+ct.Name+` WHERE id IN (`+placeholders(len(chunk))+`)`, chunk...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id int64
			vals := scalarDests(ct)
			if err := rows.Scan(append([]any{&id}, vals...)...); err != nil {
				return err
			}
			comps[id] = scalarData(ct, vals)
		}
		return rows.Err()
	})
}

func (s *SQLStore) Referrers(ctx context.Context, typ string, id int64) ([]Referrer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT owner_table, owner_id, field FROM relations WHERE target_type = ? AND target_id = ? ORDER BY owner_table, owner_id`, typ, id)
	if err != nil {
		return nil, err
	}
	type ref struct {
		table string
		id    int64
		field string
	}
	var refs []ref
	for rows.Next() {
		var r ref
		if err := rows.Scan(&r.table, &r.id, &r.field); err != nil {
			rows.Close()
			return nil, err
		}
		refs = append(refs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var out []Referrer
	seen := map[string]bool{}
	for _, r := range refs {
		table, ownerID, nested := r.table, r.id, false
		// Walk up from components to the owning entry.
		for depth := 0; ; depth++ {
			if t, ok := s.layout.tables[table]; ok && t.Entry {
				break
			}
			if depth > 100 {
				return nil, fmt.Errorf("storage: component chain too deep at %s #%d", table, ownerID)
			}
			if err := s.db.QueryRowContext(ctx, `SELECT owner_table, owner_id FROM components WHERE component_table = ? AND component_id = ?`, table, ownerID).Scan(&table, &ownerID); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					table = ""
					break
				}
				return nil, err
			}
			nested = true
		}
		t, ok := s.layout.tables[table]
		if !ok {
			continue
		}
		key := fmt.Sprint(table, ownerID, r.field)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Referrer{Type: t.Type, ID: ownerID, Field: r.field, Nested: nested})
	}
	return out, nil
}

// --- helpers ---

func (s *SQLStore) inChunks(ids []any, fn func([]any) error) error {
	const size = 500
	for i := 0; i < len(ids); i += size {
		end := i + size
		if end > len(ids) {
			end = len(ids)
		}
		if err := fn(ids[i:end]); err != nil {
			return err
		}
	}
	return nil
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func fieldOf(t *table, name string) (content.Field, bool) {
	for _, f := range t.Fields {
		if f.Name == name {
			return f, true
		}
	}
	return content.Field{}, false
}

func asItems(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func relationIDs(v any) []int64 {
	switch x := v.(type) {
	case int64:
		return []int64{x}
	case float64:
		return []int64{int64(x)}
	case []any:
		var out []int64
		for _, it := range x {
			out = append(out, relationIDs(it)...)
		}
		return out
	}
	return nil
}

func scalarArg(c column, v any) any {
	if v == nil {
		return nil
	}
	if c.Storage == content.StoreBool {
		if b, _ := v.(bool); b {
			return 1
		}
		return 0
	}
	return v
}

func scalarDests(t *table) []any {
	out := make([]any, len(t.Columns))
	for i, c := range t.Columns {
		switch c.Storage {
		case content.StoreNumber:
			out[i] = new(sql.NullFloat64)
		case content.StoreBool:
			out[i] = new(sql.NullInt64)
		default:
			out[i] = new(sql.NullString)
		}
	}
	return out
}

func scalarData(t *table, vals []any) map[string]any {
	m := map[string]any{}
	for i, c := range t.Columns {
		switch v := vals[i].(type) {
		case *sql.NullString:
			if v.Valid {
				m[c.Field.Name] = v.String
			}
		case *sql.NullFloat64:
			if v.Valid {
				m[c.Field.Name] = v.Float64
			}
		case *sql.NullInt64:
			m[c.Field.Name] = v.Valid && v.Int64 != 0
		}
	}
	return m
}
