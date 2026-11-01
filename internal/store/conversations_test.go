package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/OrestisStefanou/Loci/internal/domain"
	"github.com/OrestisStefanou/Loci/internal/store"
	"github.com/OrestisStefanou/Loci/internal/testutil"
)

func ptr[T any](v T) *T { return &v }

func TestEnsureConversationKey(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	row := func(id, session, agent string) store.ConversationRow {
		return store.ConversationRow{ID: id, Harness: "claude-code", SessionID: session, AgentID: agent, StartedAt: testutil.Epoch}
	}

	first, created, err := s.EnsureConversation(ctx, row("c1", "s1", ""))
	if err != nil || !created || first.ID != "c1" {
		t.Fatalf("first: %+v created=%v err=%v", first, created, err)
	}
	again, created, err := s.EnsureConversation(ctx, row("c2", "s1", ""))
	if err != nil || created || again.ID != "c1" {
		t.Fatalf("same key: %+v created=%v err=%v", again, created, err)
	}
	for _, tt := range []struct{ name, id, session, agent string }{
		{"other agent in the session", "c3", "s1", "a1"},
		{"other session", "c4", "s2", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c, created, err := s.EnsureConversation(ctx, row(tt.id, tt.session, tt.agent))
			if err != nil || !created || c.ID != tt.id {
				t.Fatalf("%+v created=%v err=%v", c, created, err)
			}
		})
	}
	if _, err := s.FindConversation(ctx, "claude-code", "nope", ""); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestConversationEffectiveAttachment(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	mustProject := func(id, slug string) {
		if _, err := s.EnsureProject(ctx, domain.Project{ID: id, Slug: slug, CreatedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
	}
	mustProject("p-own", "own")
	mustProject("p-task", "taskproj")
	if _, err := s.EnsureTask(ctx, domain.Task{ID: "t1", Key: "EPD-1", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetTaskProject(ctx, "t1", ptr("p-task")); err != nil {
		t.Fatal(err)
	}
	ensure := func(r store.ConversationRow) domain.Conversation {
		r.Harness, r.SessionID, r.StartedAt = "h", "s", testutil.Epoch
		c, _, err := s.EnsureConversation(ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	parent := ensure(store.ConversationRow{ID: "c1", ProjectID: ptr("p-own"), TaskID: ptr("t1")})
	inherits := ensure(store.ConversationRow{ID: "c2", AgentID: "a1", ParentID: ptr("c1")})
	own := ensure(store.ConversationRow{ID: "c3", AgentID: "a2", ParentID: ptr("c1"), ProjectID: ptr("p-own")})

	tests := []struct {
		name          string
		got           domain.Conversation
		project, task string
	}{
		{"project follows the task", parent, "taskproj", "EPD-1"},
		{"subagent run without its own takes the parent's", inherits, "taskproj", "EPD-1"},
		{"subagent run with its own keeps it", own, "own", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.Project != tt.project || tt.got.Task != tt.task {
				t.Fatalf("got %q/%q, want %q/%q", tt.got.Project, tt.got.Task, tt.project, tt.task)
			}
		})
	}
	if inherits.Parent != "c1" || inherits.Agent != "a1" {
		t.Fatalf("subagent link: %+v", inherits)
	}

	// Filters match the same effective attachment the rows show.
	filters := []struct {
		name string
		f    store.ConversationFilter
		want []domain.ID
	}{
		{"project matches through the task, own project ignored", store.ConversationFilter{ProjectID: ptr("p-own"), IncludeSubagents: true}, []domain.ID{"c3"}},
		{"project reaches runs that inherit it", store.ConversationFilter{ProjectID: ptr("p-task"), IncludeSubagents: true}, []domain.ID{"c2", "c1"}},
		{"task reaches runs that inherit it", store.ConversationFilter{TaskID: ptr("t1"), IncludeSubagents: true}, []domain.ID{"c2", "c1"}},
		{"sessions only by default", store.ConversationFilter{TaskID: ptr("t1")}, []domain.ID{"c1"}},
		{"runs of one session", store.ConversationFilter{ParentID: ptr("c1")}, []domain.ID{"c3", "c2"}},
		{"one run by id", store.ConversationFilter{ID: ptr("c2")}, []domain.ID{"c2"}},
	}
	for _, tt := range filters {
		t.Run(tt.name, func(t *testing.T) {
			got, err := s.ListConversations(ctx, tt.f)
			if err != nil {
				t.Fatal(err)
			}
			ids := []domain.ID{}
			for _, c := range got {
				ids = append(ids, c.ID)
			}
			if !slices.Equal(ids, tt.want) {
				t.Fatalf("got %v, want %v", ids, tt.want)
			}
		})
	}
}

func TestConversationProvenanceRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	c, _, err := s.EnsureConversation(ctx, store.ConversationRow{ID: "c1", Harness: "h", SessionID: "s",
		AgentType: "tech-pm-agent", Repo: "github.com/northwind/api", Branch: "main", StartedAt: testutil.Epoch})
	if err != nil {
		t.Fatal(err)
	}
	if c.Repo != "github.com/northwind/api" || c.Branch != "main" || c.AgentType != "tech-pm-agent" || !c.StartedAt.Equal(testutil.Epoch) {
		t.Fatalf("got %+v", c)
	}
	if _, err := s.GetConversation(ctx, "nope"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get unknown: want ErrNotFound, got %v", err)
	}
}

func TestInsertMessageNumbersPerConversation(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	for _, id := range []string{"c1", "c2"} {
		if _, _, err := s.EnsureConversation(ctx, store.ConversationRow{ID: id, Harness: "h", SessionID: id, StartedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
	}
	insert := func(id, conv, prompt string, role domain.Role) {
		if err := s.InsertMessage(ctx, store.MessageRow{ID: id, ConversationID: conv, PromptID: prompt, Role: role,
			Content: id, CreatedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
	}
	insert("m1", "c1", "p1", domain.RoleUser)
	insert("m2", "c1", "p1", domain.RoleAssistant)
	insert("m3", "c2", "", domain.RoleUser)

	for id, want := range map[string]int{"m1": 1, "m2": 2, "m3": 1} {
		m, err := s.GetMessage(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if m.Seq != want {
			t.Fatalf("%s: seq %d, want %d", id, m.Seq, want)
		}
	}

	m, err := s.FindPromptMessage(ctx, "c1", "p1", domain.RoleAssistant)
	if err != nil || m.ID != "m2" {
		t.Fatalf("find by prompt: %+v, %v", m, err)
	}
	if _, err := s.FindPromptMessage(ctx, "c2", "p1", domain.RoleUser); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("other conversation: want ErrNotFound, got %v", err)
	}
}

func TestAttachIfUnattached(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	if _, err := s.EnsureProject(ctx, domain.Project{ID: "p1", Slug: "fees-v2", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureProject(ctx, domain.Project{ID: "p2", Slug: "other", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.EnsureConversation(ctx, store.ConversationRow{ID: "c1", Harness: "h", SessionID: "s", StartedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		project string
		applied bool
	}{
		{"unattached is attached", "p1", true},
		{"attached is left alone", "p2", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			applied, err := s.AttachIfUnattached(ctx, "c1", ptr(tt.project), nil)
			if err != nil || applied != tt.applied {
				t.Fatalf("applied=%v err=%v", applied, err)
			}
			c, err := s.GetConversation(ctx, "c1")
			if err != nil || c.Project != "fees-v2" {
				t.Fatalf("project %q, err %v", c.Project, err)
			}
		})
	}
}

func TestSetMessageContentClearsEmbeddingAndFailure(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	if _, _, err := s.EnsureConversation(ctx, store.ConversationRow{ID: "c1", Harness: "h", SessionID: "s", StartedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertMessage(ctx, store.MessageRow{ID: "m1", ConversationID: "c1", Role: domain.RoleAssistant,
		Content: "draft", CreatedAt: testutil.Epoch}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB().ExecContext(ctx, `UPDATE messages SET embedding = vector32('[1, 0]'), embedding_model = 'm', embedding_failed = 'm' WHERE id = 'm1'`); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMessageContent(ctx, "m1", "final"); err != nil {
		t.Fatal(err)
	}
	var model, failed *string
	var embedded bool
	if err := s.DB().QueryRowContext(ctx, `SELECT embedding_model, embedding IS NOT NULL, embedding_failed FROM messages WHERE id = 'm1'`).
		Scan(&model, &embedded, &failed); err != nil {
		t.Fatal(err)
	}
	if model != nil || embedded || failed != nil {
		t.Fatalf("embedding kept: model %v, embedded %v, failed %v", model, embedded, failed)
	}
}

func TestListConversationsByVector(t *testing.T) {
	ctx := context.Background()
	s := testutil.NewStore(t)
	conv := func(id, session string, parent *string) {
		r := store.ConversationRow{ID: id, Harness: "h", SessionID: session, ParentID: parent, StartedAt: testutil.Epoch}
		if parent != nil {
			r.AgentID = id
		}
		if _, _, err := s.EnsureConversation(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	msg := func(id, conv, model string, vec []float32) {
		if err := s.InsertMessage(ctx, store.MessageRow{ID: id, ConversationID: conv, Role: domain.RoleUser,
			Content: "content of " + id, CreatedAt: testutil.Epoch}); err != nil {
			t.Fatal(err)
		}
		if vec != nil {
			if err := s.SetEmbedding(ctx, store.EmbedMessages, id, vec, model); err != nil {
				t.Fatal(err)
			}
		}
	}
	conv("c1", "s1", nil)
	conv("r1", "s1", ptr("c1"))
	conv("c2", "s2", nil)
	conv("c3", "s3", nil)
	conv("c4", "s4", nil)
	msg("m1", "c1", "m", []float32{0, 1})
	msg("m2", "r1", "m", []float32{1, 0.1}) // c1's nearest, through its run
	msg("m3", "c2", "m", []float32{0.7, 0.7})
	msg("m4", "c3", "other", []float32{1, 0}) // another model's vector is not compared
	msg("m5", "c4", "", nil)                  // not embedded

	list := func(f store.ConversationFilter) []domain.ConversationSummary {
		t.Helper()
		f.QueryVector, f.Model = []float32{1, 0}, "m"
		got, err := s.ListConversations(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	matches := func(cs []domain.ConversationSummary) []string {
		out := []string{}
		for _, c := range cs {
			out = append(out, c.ID+":"+c.Match.Conversation+"/"+c.Match.Message)
		}
		return out
	}

	t.Run("orders by each conversation's nearest message and names it", func(t *testing.T) {
		got := list(store.ConversationFilter{})
		if want := []string{"c1:r1/m2", "c2:c2/m3"}; !slices.Equal(matches(got), want) {
			t.Fatalf("got %v, want %v", matches(got), want)
		}
		if got[0].Match.Snippet != "content of m2" {
			t.Fatalf("snippet %q", got[0].Match.Snippet)
		}
	})
	t.Run("max distance leaves out conversations with nothing close", func(t *testing.T) {
		if got := matches(list(store.ConversationFilter{MaxDistance: 0.1})); !slices.Equal(got, []string{"c1:r1/m2"}) {
			t.Fatalf("got %v", got)
		}
	})
	t.Run("a subagent run listed on its own matches its own messages", func(t *testing.T) {
		// c1 and r1 tie on distance and start, so the greater id goes first.
		got := matches(list(store.ConversationFilter{IncludeSubagents: true}))
		if want := []string{"r1:r1/m2", "c1:r1/m2", "c2:c2/m3"}; !slices.Equal(got, want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("limit takes the nearest", func(t *testing.T) {
		if got := matches(list(store.ConversationFilter{Limit: 1})); !slices.Equal(got, []string{"c1:r1/m2"}) {
			t.Fatalf("got %v", got)
		}
	})
}

func TestVectorBlobIsTheStoredFormat(t *testing.T) {
	s := testutil.NewStore(t)
	vec := []float32{1.5, -2, 0.25}
	var same bool
	var distance float64
	if err := s.DB().QueryRowContext(context.Background(),
		`SELECT vector32('[1.5,-2,0.25]') = ?, vector_distance_cos(vector32('[1.5,-2,0.25]'), ?)`,
		store.VectorBlob(vec), store.VectorBlob(vec)).Scan(&same, &distance); err != nil {
		t.Fatal(err)
	}
	if !same || distance > 1e-6 {
		t.Fatalf("blob differs from vector32: equal %v, distance %v", same, distance)
	}
}
