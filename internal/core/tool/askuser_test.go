package tool

import (
	"context"
	"testing"
)

func TestAskUserToolDef(t *testing.T) {
	tl := &AskUserTool{}
	def := tl.Def()
	if def.Name != "ask_user_question" {
		t.Errorf("name = %q, want ask_user_question", def.Name)
	}
	if len(def.Parameters) == 0 {
		t.Errorf("expected parameters schema")
	}
}

func TestAskUserToolDegradesWithoutInteractor(t *testing.T) {
	tl := &AskUserTool{}
	res, err := tl.Execute(context.Background(), `{"question":"继续吗?","options":["继续","停止"]}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.Success {
		t.Errorf("expected graceful degradation in non-interactive env")
	}
}

func TestAskUserToolWithInteractor(t *testing.T) {
	tl := &AskUserTool{}
	calls := 0
	ctx := WithAskUser(context.Background(), func(q string, opts []string) (int, error) {
		calls++
		if q != "继续吗?" {
			t.Errorf("question = %q", q)
		}
		if len(opts) != 2 || opts[0] != "继续" || opts[1] != "停止" {
			t.Errorf("options = %v", opts)
		}
		return 1, nil // pick 停止
	})
	res, err := tl.Execute(ctx, `{"question":"继续吗?","options":["继续","停止"]}`)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success, got %q", res.Error)
	}
	if calls != 1 {
		t.Errorf("interactor called %d times", calls)
	}
}
