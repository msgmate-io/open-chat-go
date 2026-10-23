package nativecoder

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCoderTodoStoreWriteReadRevision(t *testing.T) {
	store := NewCoderTodoStore()
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, err := store.Write("chat-1", []CoderTodoItem{{Content: "a"}, {Content: "b"}}, at)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if first.Revision != 1 || first.UpdatedAt != at.Format(time.RFC3339) || first.Version != CoderTodoPlanVersion {
		t.Fatalf("unexpected plan: %+v", first)
	}
	if got := first.Summary(); got.Total != 2 || got.Pending != 2 {
		t.Fatalf("summary: %+v", got)
	}
	second, err := store.Write("chat-1", []CoderTodoItem{{ID: "a", Content: "a", Status: CoderTodoStatusCompleted}}, time.Time{})
	if err != nil {
		t.Fatalf("second write: %v", err)
	}
	if second.Revision != 2 || second.UpdatedAt != first.UpdatedAt {
		t.Fatalf("expected revision bump and retained timestamp: %+v", second)
	}
	read := store.Read("chat-1")
	if len(read.Items) != 1 || read.Items[0].Status != CoderTodoStatusCompleted {
		t.Fatalf("read: %+v", read)
	}
	read.Items[0].Content = "mutated"
	if store.Read("chat-1").Items[0].Content != "a" {
		t.Fatal("Read must return a copy")
	}
	empty := store.Read("chat-9")
	if empty.Revision != 0 || len(empty.Items) != 0 {
		t.Fatalf("expected empty plan, got %+v", empty)
	}
	store.Clear("chat-1")
	if store.Read("chat-1").Revision != 0 {
		t.Fatal("Clear must drop the plan")
	}
}

func TestNormalizeCoderTodoItems(t *testing.T) {
	items, err := NormalizeCoderTodoItems([]CoderTodoItem{{Content: "  x  ", Priority: " high "}})
	if err != nil || items[0].Content != "x" || items[0].Status != CoderTodoStatusPending || items[0].Priority != "high" || items[0].ID != "1" {
		t.Fatalf("normalize: %+v err=%v", items, err)
	}
	cases := []struct {
		name  string
		items []CoderTodoItem
		want  error
	}{
		{"empty", []CoderTodoItem{{Content: " "}}, ErrCoderTodoEmptyContent},
		{"status", []CoderTodoItem{{Content: "x", Status: "doing"}}, ErrCoderTodoUnknownStatus},
		{"dup", []CoderTodoItem{{ID: "1", Content: "x"}, {ID: "1", Content: "y"}}, ErrCoderTodoDuplicateItemID},
		{"twice", []CoderTodoItem{{Content: "x", Status: CoderTodoStatusInProgress}, {Content: "y", Status: CoderTodoStatusInProgress}}, ErrCoderTodoManyInProgress},
	}
	for _, tc := range cases {
		if _, err := NormalizeCoderTodoItems(tc.items); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
	}
	many := make([]CoderTodoItem, MaxCoderTodoItems+1)
	for i := range many {
		many[i] = CoderTodoItem{Content: "x"}
	}
	if _, err := NormalizeCoderTodoItems(many); !errors.Is(err, ErrCoderTodoTooManyItems) {
		t.Fatalf("expected item cap error, got %v", err)
	}
	if _, err := NewCoderTodoStore().Write("  ", nil, time.Time{}); !errors.Is(err, ErrCoderTodoEmptyChat) {
		t.Fatalf("expected empty chat error, got %v", err)
	}
	if !CoderTodoStatusIsValid(CoderTodoStatusCancelled) || CoderTodoStatusIsValid("") {
		t.Fatal("status predicate is wrong")
	}
}

func TestRenderCoderTodoPlan(t *testing.T) {
	if got := RenderCoderTodoPlan(CoderTodoPlan{}); got != "Todo plan: none." {
		t.Fatalf("empty render: %q", got)
	}
	plan := CoderTodoPlan{Revision: 3, Items: []CoderTodoItem{
		{ID: "1", Content: "done", Status: CoderTodoStatusCompleted},
		{ID: "2", Content: "going", Status: CoderTodoStatusInProgress, Priority: "high"},
		{ID: "3", Content: "later", Status: CoderTodoStatusPending},
		{ID: "4", Content: "dropped", Status: CoderTodoStatusCancelled},
	}}
	rendered := RenderCoderTodoPlan(plan)
	for _, want := range []string{"revision 3", "1 in progress", "- [x] done", "- [ ] going (in progress) (high)", "- [ ] later", "(cancelled)"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
	if strings.Index(rendered, "going") > strings.Index(rendered, "later") {
		t.Fatal("in-progress items must render before pending ones")
	}
	if next, ok := plan.NextItem(); !ok || next.ID != "2" {
		t.Fatalf("next item: %+v ok=%v", next, ok)
	}
	if _, ok := (CoderTodoPlan{Items: []CoderTodoItem{{Content: "x", Status: CoderTodoStatusCompleted}}}).NextItem(); ok {
		t.Fatal("a finished plan has no next item")
	}
	meta := plan.Metadata()
	if meta["revision"] != 3 || meta["next_item_id"] != "2" || meta["completed"] != 1 {
		t.Fatalf("metadata: %+v", meta)
	}
	if got := RenderCoderTodoPlan(CoderTodoPlan{Revision: 2}); got != "Todo plan (revision 2): none." {
		t.Fatalf("cleared render: %q", got)
	}
}
