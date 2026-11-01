package store_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

// fixture builds a project "northwind", a task "EPD-1" in it, tags and an actor.
type fixture struct {
	s           *store.Store
	project     domain.Project
	task        domain.Task
	tags        map[string]domain.Tag
	actor       domain.Actor
	nextID      int
	nextCreated time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := context.Background()
	f := &fixture{s: testutil.NewStore(t), tags: map[string]domain.Tag{}, nextCreated: testutil.Epoch}
	var err error
	if f.project, err = f.s.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "northwind", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if f.task, err = f.s.EnsureTask(ctx, domain.Task{ID: "t1", Key: "EPD-1", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if err = f.s.SetTaskProject(ctx, f.task.ID, &f.project.ID); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"auth", "perf"} {
		if f.tags[name], err = f.s.EnsureTag(ctx, domain.Tag{ID: "tag-" + name, Name: name}); err != nil {
			t.Fatal(err)
		}
	}
	if f.actor, err = f.s.EnsureActor(ctx, domain.Actor{ID: "a1", Kind: domain.ActorHuman, Handle: "tester"}); err != nil {
		t.Fatal(err)
	}
	return f
}

// add inserts a memory. Each call is one second later than the previous.
func (f *fixture) add(t *testing.T, content string, projectID, taskID *domain.ID, tags ...string) domain.ID {
	t.Helper()
	ctx := context.Background()
	f.nextID++
	f.nextCreated = f.nextCreated.Add(time.Second)
	id := fmt.Sprintf("m%d", f.nextID)
	if err := f.s.InsertMemory(ctx, store.MemoryRow{
		ID: id, Content: content, ProjectID: projectID, TaskID: taskID, AuthorActorID: &f.actor.ID, CreatedAt: f.nextCreated,
	}); err != nil {
		t.Fatal(err)
	}
	var tagIDs []domain.ID
	for _, name := range tags {
		tagIDs = append(tagIDs, f.tags[name].ID)
	}
	if err := f.s.AddItemTags(ctx, domain.ItemMemory, id, tagIDs); err != nil {
		t.Fatal(err)
	}
	return id
}

func ids(ms []domain.Memory) []domain.ID {
	out := make([]domain.ID, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func equalIDs(a, b []domain.ID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEffectiveProjectResolvesThroughTask(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	other, _ := f.s.EnsureProject(ctx, domain.Project{ID: "p2", Slug: "other", CreatedAt: testutil.Epoch})

	viaTask := f.add(t, "on the task", nil, &f.task.ID)
	direct := f.add(t, "on the project", &f.project.ID, nil)
	// Task has a project, so the memory's own (other) is overridden.
	overridden := f.add(t, "task wins", &other.ID, &f.task.ID)

	got, err := f.s.ListMemories(ctx, store.MemoryFilter{ProjectID: &f.project.ID})
	if err != nil {
		t.Fatal(err)
	}
	if want := []domain.ID{overridden, direct, viaTask}; !equalIDs(ids(got), want) {
		t.Fatalf("got %v, want %v (most recent first)", ids(got), want)
	}
	for _, m := range got {
		if m.Project != "northwind" {
			t.Fatalf("memory %s: project %q, want northwind", m.ID, m.Project)
		}
	}

	// Clear the task's project: the direct link now applies.
	if err := f.s.SetTaskProject(ctx, f.task.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, _ = f.s.ListMemories(ctx, store.MemoryFilter{ProjectID: &f.project.ID})
	if !equalIDs(ids(got), []domain.ID{direct}) {
		t.Fatalf("after clearing the task's project: got %v", ids(got))
	}
	m, _ := f.s.GetMemory(ctx, overridden)
	if m.Project != "other" || m.Task != "EPD-1" {
		t.Fatalf("got project %q task %q", m.Project, m.Task)
	}
	m, _ = f.s.GetMemory(ctx, viaTask)
	if m.Project != "" {
		t.Fatalf("task without project: got project %q, want none", m.Project)
	}
}

func TestMemoryAttachmentThroughConversation(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	other, _ := f.s.EnsureProject(ctx, domain.Project{ID: "p2", Slug: "other", CreatedAt: testutil.Epoch})
	// conv adds a session's main conversation, or a subagent run when parent is set.
	conv := func(id string, parent, projectID, taskID *domain.ID) domain.ID {
		t.Helper()
		agent := ""
		if parent != nil {
			agent = id
		}
		c, _, err := f.s.EnsureConversation(ctx, store.ConversationRow{ID: id, Harness: "claude-code", SessionID: "s1",
			AgentID: agent, ParentID: parent, HumanActorID: &f.actor.ID,
			ProjectID: projectID, TaskID: taskID, StartedAt: testutil.Epoch})
		if err != nil {
			t.Fatal(err)
		}
		return c.ID
	}
	add := func(id, content string, conversation, projectID, taskID *domain.ID) {
		t.Helper()
		if err := f.s.InsertMemory(ctx, store.MemoryRow{ID: id, Content: content, ConversationID: conversation,
			ProjectID: projectID, TaskID: taskID, AuthorActorID: &f.actor.ID, CreatedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
	}
	session := conv("c1", nil, nil, &f.task.ID)
	run := conv("c2", &session, nil, nil)
	ownRun := conv("c3", &session, &other.ID, nil)
	add("m1", "from the session", &session, nil, nil)
	add("m2", "from a run with nothing of its own", &run, nil, nil)
	add("m3", "from a run with its own project", &ownRun, nil, nil)
	add("m4", "own project only, so the conversation's task is ignored", &session, &other.ID, nil)

	tests := []struct {
		id            domain.ID
		project, task string
	}{
		{"m1", "northwind", "EPD-1"},
		{"m2", "northwind", "EPD-1"},
		{"m3", "other", ""},
		{"m4", "other", ""},
	}
	for _, tt := range tests {
		m, err := f.s.GetMemory(ctx, tt.id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Project != tt.project || m.Task != tt.task {
			t.Errorf("%s (%s): got %q/%q, want %q/%q", tt.id, m.Content, m.Project, m.Task, tt.project, tt.task)
		}
	}
	got, err := f.s.ListMemories(ctx, store.MemoryFilter{TaskID: &f.task.ID})
	if err != nil || len(got) != 2 {
		t.Fatalf("by task: %v, %v", ids(got), err)
	}
	if got, err = f.s.ListMemories(ctx, store.MemoryFilter{ProjectID: &other.ID}); err != nil || len(got) != 2 {
		t.Fatalf("by project: %v, %v", ids(got), err)
	}
}

func TestListMemoriesFilters(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	both := f.add(t, "Settlement cut-off is 16:00", &f.project.ID, nil, "auth", "perf")
	authOnly := f.add(t, "tokens rotate daily", nil, &f.task.ID, "auth")
	none := f.add(t, "nothing tagged", nil, nil)
	if err := f.s.SetMemoryStatus(ctx, none, domain.MemoryInactive, testutil.Epoch); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		filter store.MemoryFilter
		want   []domain.ID
	}{
		{"active only by default", store.MemoryFilter{}, []domain.ID{authOnly, both}},
		{"include inactive", store.MemoryFilter{IncludeInactive: true}, []domain.ID{none, authOnly, both}},
		{"one tag", store.MemoryFilter{TagIDs: []domain.ID{f.tags["auth"].ID}}, []domain.ID{authOnly, both}},
		{"two tags means both", store.MemoryFilter{TagIDs: []domain.ID{f.tags["auth"].ID, f.tags["perf"].ID}}, []domain.ID{both}},
		{"task", store.MemoryFilter{TaskID: &f.task.ID}, []domain.ID{authOnly}},
		{"query is case-insensitive substring", store.MemoryFilter{Query: "CUT-OFF"}, []domain.ID{both}},
		{"limit", store.MemoryFilter{Limit: 1}, []domain.ID{authOnly}},
		{"no match", store.MemoryFilter{Query: "absent"}, []domain.ID{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.s.ListMemories(ctx, tt.filter)
			if err != nil {
				t.Fatal(err)
			}
			if !equalIDs(ids(got), tt.want) {
				t.Fatalf("got %v, want %v", ids(got), tt.want)
			}
			if got == nil {
				t.Fatal("want an empty slice, not nil")
			}
		})
	}

	m, err := f.s.GetMemory(ctx, both)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tags) != 2 || m.Tags[0] != "auth" || m.Tags[1] != "perf" || m.Author != "tester" {
		t.Fatalf("got %+v", m)
	}
	m, _ = f.s.GetMemory(ctx, none)
	if m.Status != domain.MemoryInactive || len(m.Tags) != 0 || m.Tags == nil {
		t.Fatalf("got %+v", m)
	}
}

func TestAddItemTagsIsIdempotent(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	id := f.add(t, "x", nil, nil, "auth")
	if err := f.s.AddItemTags(ctx, domain.ItemMemory, id, []domain.ID{f.tags["auth"].ID, f.tags["auth"].ID}); err != nil {
		t.Fatal(err)
	}
	m, _ := f.s.GetMemory(ctx, id)
	if len(m.Tags) != 1 {
		t.Fatalf("got tags %v", m.Tags)
	}
}

func TestSetItemTagsReplacesOnlyThatItem(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.add(t, "a", nil, nil, "auth")
	b := f.add(t, "b", nil, nil, "auth")
	if err := f.s.SetItemTags(ctx, domain.ItemMemory, a, []domain.ID{f.tags["perf"].ID}); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.s.GetMemory(ctx, a); !equalIDs(m.Tags, []string{"perf"}) {
		t.Fatalf("a: got tags %v, want [perf]", m.Tags)
	}
	if m, _ := f.s.GetMemory(ctx, b); !equalIDs(m.Tags, []string{"auth"}) {
		t.Fatalf("b: got tags %v, want [auth]", m.Tags)
	}
	if err := f.s.SetItemTags(ctx, domain.ItemMemory, a, nil); err != nil {
		t.Fatal(err)
	}
	if m, _ := f.s.GetMemory(ctx, a); len(m.Tags) != 0 {
		t.Fatalf("a: got tags %v, want none", m.Tags)
	}
	// The tag itself stays.
	if _, err := f.s.GetTag(ctx, "perf"); err != nil {
		t.Fatal(err)
	}
}

func TestSetMemoryProjectAndTask(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	id := f.add(t, "x", nil, nil)
	later := testutil.Epoch.Add(time.Hour)
	if err := f.s.SetMemoryProject(ctx, id, &f.project.ID, later); err != nil {
		t.Fatal(err)
	}
	m, _ := f.s.GetMemory(ctx, id)
	if m.Project != "northwind" || m.Task != "" || !m.UpdatedAt.Equal(later) {
		t.Fatalf("after set project: %+v", m)
	}
	if err := f.s.SetMemoryTask(ctx, id, &f.task.ID, later); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetMemoryProject(ctx, id, nil, later); err != nil {
		t.Fatal(err)
	}
	// The effective project now comes from the task.
	m, _ = f.s.GetMemory(ctx, id)
	if m.Project != "northwind" || m.Task != "EPD-1" {
		t.Fatalf("after set task, clear project: %+v", m)
	}
	if err := f.s.SetMemoryTask(ctx, id, nil, later); err != nil {
		t.Fatal(err)
	}
	if m, _ = f.s.GetMemory(ctx, id); m.Project != "" || m.Task != "" {
		t.Fatalf("after clearing both: %+v", m)
	}
}

func TestEnsureActorKeepsFirstKind(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	first, err := s.EnsureActor(ctx, domain.Actor{ID: "a1", Kind: domain.ActorAgent, Handle: "claude", AgentType: "main"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.EnsureActor(ctx, domain.Actor{ID: "a2", Kind: domain.ActorHuman, Handle: "claude"})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "a1" || second.ID != "a1" || second.Kind != domain.ActorAgent || second.AgentType != "main" {
		t.Fatalf("got %+v then %+v", first, second)
	}
}

func TestMemoryNotFound(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	if _, err := s.GetMemory(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get: want ErrNotFound, got %v", err)
	}
	if err := s.SetMemoryStatus(ctx, "nope", domain.MemoryInactive, testutil.Epoch); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("set status: want ErrNotFound, got %v", err)
	}
	if err := s.SetMemoryProject(ctx, "nope", nil, testutil.Epoch); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("set project: want ErrNotFound, got %v", err)
	}
	if err := s.SetMemoryTask(ctx, "nope", nil, testutil.Epoch); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("set task: want ErrNotFound, got %v", err)
	}
	if err := s.TouchMemory(ctx, "nope", testutil.Epoch); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("touch: want ErrNotFound, got %v", err)
	}
	if _, err := s.GetTag(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("tag: want ErrNotFound, got %v", err)
	}
}

func TestMemoryEmbeddings(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	a := f.add(t, "alpha", &f.project.ID, nil)
	b := f.add(t, "beta", &f.project.ID, nil)
	c := f.add(t, "gamma", &f.project.ID, nil)

	pendingIDs := func(model string) []domain.ID {
		t.Helper()
		rows, err := f.s.PendingEmbeddings(ctx, store.EmbedMemories, model, 10)
		if err != nil {
			t.Fatal(err)
		}
		out := []domain.ID{}
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}

	if got := pendingIDs("m1"); !equalIDs(got, []domain.ID{a, b, c}) {
		t.Fatalf("all pending, oldest first: got %v", got)
	}

	vecs := map[domain.ID][]float32{a: {1, 0, 0}, b: {0.7, 0.7, 0}, c: {0, 0, 1}}
	for id, v := range vecs {
		if err := f.s.SetEmbedding(ctx, store.EmbedMemories, id, v, "m1"); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("stored vector round trips", func(t *testing.T) {
		var got string
		if err := f.s.DB().QueryRowContext(ctx, `SELECT vector_extract(embedding) FROM memories WHERE id = ?`, b).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "[0.7,0.7,0]" {
			t.Fatalf("got %s", got)
		}
	})

	t.Run("pending skips rows embedded by the same model only", func(t *testing.T) {
		if got := pendingIDs("m1"); len(got) != 0 {
			t.Fatalf("m1: got %v", got)
		}
		if got := pendingIDs("m2"); !equalIDs(got, []domain.ID{a, b, c}) {
			t.Fatalf("m2: got %v", got)
		}
		n, err := f.s.CountPendingEmbeddings(ctx, store.EmbedMemories, "m2")
		if err != nil || n != 3 {
			t.Fatalf("count: %d, %v", n, err)
		}
	})

	t.Run("vector query orders by distance and skips unembedded rows", func(t *testing.T) {
		d := f.add(t, "delta, not embedded", &f.project.ID, nil)
		got, err := f.s.ListMemories(ctx, store.MemoryFilter{QueryVector: []float32{1, 0.1, 0}, Model: "m1", Query: "ignored"})
		if err != nil {
			t.Fatal(err)
		}
		if !equalIDs(ids(got), []domain.ID{a, b, c}) {
			t.Fatalf("got %v", ids(got))
		}
		got, err = f.s.ListMemories(ctx, store.MemoryFilter{QueryVector: []float32{1, 0, 0}, Model: "other"})
		if err != nil || len(got) != 0 {
			t.Fatalf("other model: got %v, %v", ids(got), err)
		}
		if got := pendingIDs("m1"); !equalIDs(got, []domain.ID{d}) {
			t.Fatalf("pending: got %v", got)
		}
	})

	t.Run("max distance drops far memories", func(t *testing.T) {
		// Distances from [1,0,0]: alpha 0, beta about 0.29, gamma 1.
		got, err := f.s.ListMemories(ctx, store.MemoryFilter{QueryVector: []float32{1, 0, 0}, Model: "m1", MaxDistance: 0.4})
		if err != nil {
			t.Fatal(err)
		}
		if !equalIDs(ids(got), []domain.ID{a, b}) {
			t.Fatalf("got %v", ids(got))
		}
	})

	t.Run("missing memory is not found", func(t *testing.T) {
		if err := f.s.SetEmbedding(ctx, store.EmbedMemories, "nope", []float32{1}, "m1"); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("clear counts the rows it cleared", func(t *testing.T) {
		n, err := f.s.ClearEmbeddings(ctx, store.EmbedMemories)
		if err != nil || n != 3 {
			t.Fatalf("got %d, %v", n, err)
		}
		if got := pendingIDs("m1"); len(got) != 4 {
			t.Fatalf("pending after clear: %v", got)
		}
	})
}

func TestBlankMemoriesAreNeverPending(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.add(t, " \n ", nil, nil) // oldest, so it would head the queue
	real := f.add(t, "real", nil, nil)
	rows, err := f.s.PendingEmbeddings(ctx, store.EmbedMemories, "m1", 1)
	if err != nil || len(rows) != 1 || rows[0].ID != real {
		t.Fatalf("got %v, %v", rows, err)
	}
	if n, err := f.s.CountPendingEmbeddings(ctx, store.EmbedMemories, "m1"); err != nil || n != 1 {
		t.Fatalf("count %d, %v", n, err)
	}
}

// Every filter at once with a vector query: placeholders are appended across
// WHERE and ORDER BY, so a wrong order would bind values to the wrong slots.
func TestVectorQueryWithEveryFilter(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	near := f.add(t, "near, on task, both tags", &f.project.ID, &f.task.ID, "auth", "perf")
	mid := f.add(t, "mid, on task, both tags", &f.project.ID, &f.task.ID, "auth", "perf")
	far := f.add(t, "far, on task, both tags", &f.project.ID, &f.task.ID, "auth", "perf")
	oneTag := f.add(t, "near, on task, one tag", &f.project.ID, &f.task.ID, "auth")
	offTask := f.add(t, "near, project only, both tags", &f.project.ID, nil, "auth", "perf")
	inactive := f.add(t, "near, on task, both tags, inactive", &f.project.ID, &f.task.ID, "auth", "perf")
	if err := f.s.SetMemoryStatus(ctx, inactive, domain.MemoryInactive, testutil.Epoch); err != nil {
		t.Fatal(err)
	}
	vecs := map[domain.ID][]float32{
		near: {1, 0, 0}, mid: {0.8, 0.6, 0}, far: {0, 0, 1},
		oneTag: {1, 0, 0}, offTask: {1, 0, 0}, inactive: {1, 0, 0},
	}
	for id, v := range vecs {
		if err := f.s.SetEmbedding(ctx, store.EmbedMemories, id, v, "m1"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := f.s.ListMemories(ctx, store.MemoryFilter{
		ProjectID: &f.project.ID, TaskID: &f.task.ID,
		TagIDs:      []domain.ID{f.tags["auth"].ID, f.tags["perf"].ID},
		QueryVector: []float32{1, 0, 0}, Model: "m1", MaxDistance: 0.5,
		Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	// mid is at distance 0.2, far at 1 (beyond 0.5); the rest fail a filter.
	if !equalIDs(ids(got), []domain.ID{near, mid}) {
		t.Fatalf("got %v", ids(got))
	}

	got, err = f.s.ListMemories(ctx, store.MemoryFilter{
		TaskID: &f.task.ID, QueryVector: []float32{1, 0, 0}, Model: "m1", MaxDistance: 0.5, Limit: 1,
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("limit: got %v, %v", ids(got), err)
	}
}
