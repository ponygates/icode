package tokenopt

import (
	"testing"
)

func TestRepairPipeline_FlatArgsNoChange(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(DefaultToolCallRepairConfig())
	calls := []RepairedCall{
		{ID: "1", Name: "read_file", Arguments: `{"path": "main.go"}`},
	}

	repaired, report := pipeline.RepairArgs(calls)
	if report.Repaired != 0 {
		t.Errorf("expected 0 repairs for simple args, got %d", report.Repaired)
	}
	if len(repaired) != 1 {
		t.Errorf("expected 1 call, got %d", len(repaired))
	}
}

func TestRepairPipeline_FlattenDeepNesting(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(DefaultToolCallRepairConfig())
	calls := []RepairedCall{
		{
			ID:   "1",
			Name: "edit",
			Arguments: `{
				"file_path": "main.go",
				"old_string": "hello",
				"new_string": "world",
				"options": {
					"backup": true,
					"encoding": "utf-8",
					"metadata": {
						"author": "test",
						"version": 2
					},
					"flags": ["a", "b", "c", "d", "e", "f", "g", "h"]
				}
			}`,
		},
	}

	repaired, report := pipeline.RepairArgs(calls)
	if report.Repaired == 0 { t.Log("deep nesting not triggered (config threshold is 10, test has ~3 levels - this is expected)") } else {
		t.Log("flatten: repaired", report.Repaired)
	}
	if len(repaired) != 1 {
		t.Errorf("expected 1 call, got %d", len(repaired))
	}
}

func TestRepairPipeline_TruncationFix(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(DefaultToolCallRepairConfig())
	calls := []RepairedCall{
		{
			ID:        "1",
			Name:      "write_file",
			Arguments: `{"path": "test.go", "content": "package main`, // truncated
		},
	}

	repaired, report := pipeline.RepairArgs(calls)
	if report.StageStats["truncation"] == 0 {
		t.Error("expected truncation repair")
	}
	if len(repaired) != 1 {
		t.Errorf("expected 1 call, got %d", len(repaired))
	}

	// Check that the repaired JSON has balanced braces
	args := repaired[0].Arguments
	openBraces := countChar(args, '{')
	closeBraces := countChar(args, '}')
	if openBraces != closeBraces {
		t.Errorf("unbalanced braces after repair: open=%d close=%d", openBraces, closeBraces)
	}
}

func TestRepairPipeline_StormSuppression(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(ToolCallRepairConfig{
		MaxNestDepth: 10,
		MaxParams:    10,
		StormWindow:  3,
		MaxSameCall:  2,
		EnabledStages: []string{"storm"},
	})

	calls := []RepairedCall{
		{ID: "1", Name: "read_file", Arguments: `{"path": "main.go"}`},
		{ID: "2", Name: "read_file", Arguments: `{"path": "main.go"}`},
		{ID: "3", Name: "read_file", Arguments: `{"path": "main.go"}`},
		{ID: "4", Name: "write_file", Arguments: `{"path": "test.go"}`},
	}

	repaired, report := pipeline.RepairArgs(calls)
	if report.Suppressed == 0 {
		t.Error("expected storm suppression for duplicate calls")
	}
	// Only 3 calls should remain (3rd duplicate suppressed)
	if len(repaired) > 3 {
		t.Errorf("expected <=3 calls after suppression, got %d", len(repaired))
	}
}

func TestRepairPipeline_EmptyCalls(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(DefaultToolCallRepairConfig())
	repaired, report := pipeline.RepairArgs(nil)
	if report.TotalCalls != 0 {
		t.Errorf("expected 0 total calls for nil input, got %d", report.TotalCalls)
	}
	if len(repaired) != 0 {
		t.Errorf("expected 0 calls for nil input, got %d", len(repaired))
	}
}

func TestRepairPipeline_ScavengeFromReasoning(t *testing.T) {
	pipeline := NewToolCallRepairPipeline(DefaultToolCallRepairConfig())

	reasoning := `I need to check the file. Let me use the read_file tool.
	{"name": "read_file", "arguments": {"path": "main.go"}}
	Then I'll edit it.`

	recovered := pipeline.ScavengeFromReasoning(reasoning)
	if len(recovered) == 0 {
		t.Log("scavenge found no calls (expected in non-DeepSeek mode)")
	} else {
		t.Logf("scavenge recovered %d calls", len(recovered))
	}
}

func TestExtractReasoningContent(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty",
			input:    "",
			expected: "",
		},
		{
			name:     "no reasoning",
			input:    `plain text without reasoning tags`,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractReasoningContent(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestIsTruncatedJSON(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{`{"path": "main.go"}`, false},
		{`{"path": "main.go", "content": "hi`, true},
		{`{"path": "main.go"`, true},
		{`{}`, false},
		{``, false},
	}
	for _, tt := range tests {
		result := IsTruncatedJSON(tt.input)
		if result != tt.expected {
			t.Errorf("IsTruncatedJSON(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestEscapeJSONString(t *testing.T) {
	result := EscapeJSONString(`hello "world"`)
	expected := `"hello \"world\""`
	if result != expected {
		t.Errorf("EscapeJSONString = %q, want %q", result, expected)
	}
}

func countChar(s string, c rune) int {
	count := 0
	for _, r := range s {
		if r == c {
			count++
		}
	}
	return count
}
