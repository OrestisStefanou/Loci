package store

import (
	"context"
	"fmt"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// EnsureProject inserts p if no project with p.Slug exists and returns the
// stored project either way. This is lazy creation: projects come into
// existence on first mention (product.md section 7).
func (q queries) EnsureProject(ctx context.Context, p domain.Project) (domain.Project, error) {
	if _, err := q.db.ExecContext(ctx,
		`INSERT INTO projects (id, slug, created_at) VALUES (?, ?, ?) ON CONFLICT (slug) DO NOTHING`,
		p.ID, p.Slug, formatTime(p.CreatedAt)); err != nil {
		return domain.Project{}, fmt.Errorf("insert project %q: %w", p.Slug, err)
	}
	return q.GetProject(ctx, p.Slug)
}

// GetProject returns the project with the given slug, or domain.ErrNotFound.
func (q queries) GetProject(ctx context.Context, slug string) (domain.Project, error) {
	var p domain.Project
	var created string
	err := q.db.QueryRowContext(ctx,
		`SELECT id, slug, created_at FROM projects WHERE slug = ?`, slug).
		Scan(&p.ID, &p.Slug, &created)
	if isNoRows(err) {
		return domain.Project{}, fmt.Errorf("project %q: %w", slug, domain.ErrNotFound)
	}
	if err != nil {
		return domain.Project{}, fmt.Errorf("get project %q: %w", slug, err)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return domain.Project{}, fmt.Errorf("project %q: %w", slug, err)
	}
	return p, nil
}

// ListProjects returns projects whose slug contains query (all when empty),
// ordered by slug.
func (q queries) ListProjects(ctx context.Context, query string) ([]domain.Project, error) {
	rows, err := q.db.QueryContext(ctx,
		`SELECT id, slug, created_at FROM projects WHERE instr(slug, ?) > 0 ORDER BY slug`, query)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	out := []domain.Project{}
	for rows.Next() {
		var p domain.Project
		var created string
		if err := rows.Scan(&p.ID, &p.Slug, &created); err != nil {
			return nil, fmt.Errorf("scan project: %w", err)
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, fmt.Errorf("project %q: %w", p.Slug, err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
