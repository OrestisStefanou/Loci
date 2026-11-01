package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/OrestisStefanou/Loci/internal/domain"
)

// ListInput is the input of List (MCP: loci_list).
type ListInput struct {
	Kind  domain.Kind `json:"kind" jsonschema:"one of projects, tasks, tags"`
	Query string      `json:"query,omitempty" jsonschema:"only return names containing this text"`
}

// ListItem is one project, task or tag. Project is set only for tasks that
// belong to one.
type ListItem struct {
	ID      domain.ID `json:"id"`
	Name    string    `json:"name"`
	Project string    `json:"project,omitempty"`
}

// ListOutput is the output of List.
type ListOutput struct {
	Kind  domain.Kind `json:"kind"`
	Items []ListItem  `json:"items"`
}

// List lists or searches projects, tasks or tags by name. Tasks come most
// recently used first, so the one being worked on is at the top.
func (a *App) List(ctx context.Context, in ListInput) (ListOutput, error) {
	out := ListOutput{Kind: in.Kind, Items: []ListItem{}}
	query := strings.TrimSpace(in.Query)

	switch in.Kind {
	case domain.KindProject:
		ps, err := a.store.ListProjects(ctx, strings.ToLower(query))
		if err != nil {
			return ListOutput{}, err
		}
		for _, p := range ps {
			out.Items = append(out.Items, ListItem{ID: p.ID, Name: p.Slug})
		}
	case domain.KindTask:
		ts, err := a.store.ListTasks(ctx, strings.ToUpper(query))
		if err != nil {
			return ListOutput{}, err
		}
		for _, t := range ts {
			out.Items = append(out.Items, ListItem{ID: t.ID, Name: t.Key, Project: t.ProjectSlug})
		}
	case domain.KindTag:
		tags, err := a.store.ListTags(ctx, strings.ToLower(query))
		if err != nil {
			return ListOutput{}, err
		}
		for _, t := range tags {
			out.Items = append(out.Items, ListItem{ID: t.ID, Name: t.Name})
		}
	default:
		return ListOutput{}, fmt.Errorf("kind %q must be one of projects, tasks, tags: %w", in.Kind, domain.ErrInvalid)
	}
	return out, nil
}
