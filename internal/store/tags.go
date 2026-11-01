package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// EnsureTag inserts t if no tag with t.Name exists and returns the stored tag
// either way.
func (q queries) EnsureTag(ctx context.Context, t domain.Tag) (domain.Tag, error) {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO tags (id, name) VALUES (?, ?) ON CONFLICT (name) DO NOTHING`,
		t.ID, t.Name); err != nil {
		return domain.Tag{}, fmt.Errorf("insert tag %q: %w", t.Name, err)
	}
	var out domain.Tag
	if err := q.db.QueryRowContext(ctx,
		`SELECT id, name FROM tags WHERE name = ?`, t.Name).Scan(&out.ID, &out.Name); err != nil {
		return domain.Tag{}, fmt.Errorf("get tag %q: %w", t.Name, err)
	}
	return out, nil
}

// ListTags returns tags whose name contains query (all when empty), ordered by name.
func (q queries) ListTags(ctx context.Context, query string) ([]domain.Tag, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, name FROM tags WHERE instr(name, ?) > 0 ORDER BY name`, query)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer rows.Close()
	out := []domain.Tag{}
	for rows.Next() {
		var t domain.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
			return nil, fmt.Errorf("scan tag: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTag returns the tag with the given name, or domain.ErrNotFound.
func (q queries) GetTag(ctx context.Context, name string) (domain.Tag, error) {
	var t domain.Tag
	err := q.db.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name = ?`, name).Scan(&t.ID, &t.Name)
	if isNoRows(err) {
		return domain.Tag{}, fmt.Errorf("tag %q: %w", name, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Tag{}, fmt.Errorf("get tag %q: %w", name, err)
	}
	return t, nil
}

// AddItemTags attaches tags to an item. Tags already attached are left alone.
func (q queries) AddItemTags(ctx context.Context, kind domain.ItemKind, itemID domain.ID, tagIDs []domain.ID) error {
	for _, tagID := range tagIDs {
		if _, err := q.db.ExecContext(ctx,
			`INSERT INTO item_tags (item_kind, item_id, tag_id) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			kind, itemID, tagID); err != nil {
			return fmt.Errorf("tag %s %s: %w", kind, itemID, err)
		}
	}
	return nil
}

// SetItemTags replaces an item's tags with tagIDs. Tags left unused stay in
// the tags table: a tag is an identifier and is never deleted.
func (q queries) SetItemTags(ctx context.Context, kind domain.ItemKind, itemID domain.ID, tagIDs []domain.ID) error {
	if _, err := q.db.ExecContext(ctx,
		`DELETE FROM item_tags WHERE item_kind = ? AND item_id = ?`, kind, itemID); err != nil {
		return fmt.Errorf("untag %s %s: %w", kind, itemID, err)
	}
	return q.AddItemTags(ctx, kind, itemID, tagIDs)
}

// ItemTags returns the tag names of each given item, sorted, in one query.
// Items without tags are absent from the map.
func (q queries) ItemTags(ctx context.Context, kind domain.ItemKind, itemIDs []domain.ID) (map[domain.ID][]string, error) {
	out := map[domain.ID][]string{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	args := []any{kind}
	for _, id := range itemIDs {
		args = append(args, id)
	}
	rows, err := q.db.QueryContext(ctx,
		`SELECT it.item_id, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
		WHERE it.item_kind = ? AND it.item_id IN (`+placeholders(len(itemIDs))+`)
		ORDER BY it.item_id, t.name`, args...)
	if err != nil {
		return nil, fmt.Errorf("list item tags: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id domain.ID
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, fmt.Errorf("scan item tag: %w", err)
		}
		out[id] = append(out[id], name)
	}
	return out, rows.Err()
}

// placeholders returns "?, ?, ..." with n entries.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}
