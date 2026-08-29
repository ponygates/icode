package tool

import (
	"context"
	"testing"
)

func TestAskUserFormToolDegradesWithoutInteractor(t *testing.T) {
	tl := &AskUserFormTool{}
	res, err := tl.Execute(context.Background(), `{"questions":[{"question":"q1","options":["a","b"]}]}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Success {
		t.Errorf("expected graceful degradation in non-interactive env")
	}
}

func TestAskUserFormToolWithInteractor(t *testing.T) {
	tl := &AskUserFormTool{}
	ctx := WithAskUserForm(context.Background(), func(qs []FormQuestion) ([]FormAnswer, error) {
		if len(qs) != 2 {
			t.Errorf("questions = %d, want 2", len(qs))
		}
		return []FormAnswer{{Index: 1}, {Indices: []int{0, 2}}}, nil
	})
	res, err := tl.Execute(ctx, `{"questions":[{"question":"q1","options":["a","b"]},{"question":"q2","options":["x","y","z"],"multi_select":true}]}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success, got %q", res.Error)
	}
	if res.Content != `[{"index":1},{"indices":[0,2]}]` {
		t.Errorf("content = %q", res.Content)
	}
}

func TestAskUserFormToolTooManyQuestions(t *testing.T) {
	tl := &AskUserFormTool{}
	qs := make([]map[string]any, 10)
	for i := range qs {
		qs[i] = map[string]any{"question": "q"}
	}
	args := `{"questions":` + `[{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"},{"question":"q"}]` + `}`
	ctx := WithAskUserForm(context.Background(), func(qs []FormQuestion) ([]FormAnswer, error) {
		if len(qs) > 8 {
			t.Errorf("cap 8 violated: got %d", len(qs))
		}
		return make([]FormAnswer, len(qs)), nil
	})
	if _, err := tl.Execute(ctx, args); err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
}
