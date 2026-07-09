package proxy

import "testing"

// buildMultiCycleClaudeReq builds a conversation with two COMPLETED tool cycles
// in history (each: assistant tool_use + user tool_result) followed by a final
// plain-text user instruction. This is the shape a long agentic loop produces.
func buildMultiCycleClaudeReq() *ClaudeRequest {
	return &ClaudeRequest{
		Model: "claude-opus-4.8",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "start the task"},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "t1", "name": "Task", "input": map[string]interface{}{"description": "sub 1"}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "t1", "content": "sub 1 done"},
			}},
			{Role: "assistant", Content: []interface{}{
				map[string]interface{}{"type": "tool_use", "id": "t2", "name": "Task", "input": map[string]interface{}{"description": "sub 2"}},
			}},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "t2", "content": "sub 2 done"},
			}},
			{Role: "user", Content: "now summarize"},
		},
	}
}

func countStructuredToolUseTurns(hist []KiroHistoryMessage) int {
	n := 0
	for _, h := range hist {
		if h.AssistantResponseMessage != nil && len(h.AssistantResponseMessage.ToolUses) > 0 {
			n++
		}
	}
	return n
}

func countStructuredToolResultTurns(hist []KiroHistoryMessage) int {
	n := 0
	for _, h := range hist {
		if h.UserInputMessage != nil && h.UserInputMessage.UserInputMessageContext != nil &&
			len(h.UserInputMessage.UserInputMessageContext.ToolResults) > 0 {
			n++
		}
	}
	return n
}

// TestKeepPairedPreservesMultipleToolCycles verifies that with keepPaired=true,
// every correctly-paired tool cycle in history keeps its structured toolUses and
// toolResults — not just the last active turn.
func TestKeepPairedPreservesMultipleToolCycles(t *testing.T) {
	payload := ClaudeToKiroWithHistoryMode(buildMultiCycleClaudeReq(), false, true)
	hist := payload.ConversationState.History

	if got := countStructuredToolUseTurns(hist); got != 2 {
		t.Fatalf("keepPaired: expected 2 structured tool-use turns, got %d", got)
	}
	if got := countStructuredToolResultTurns(hist); got != 2 {
		t.Fatalf("keepPaired: expected 2 structured tool-result turns, got %d", got)
	}
}

// TestSafeModeFlattensAllButActive verifies the fallback (keepPaired=false) still
// flattens history: since the final message is plain text (no current tool
// results), NO history turn keeps structured tool data.
func TestSafeModeFlattensAllButActive(t *testing.T) {
	payload := ClaudeToKiroWithHistoryMode(buildMultiCycleClaudeReq(), false, false)
	hist := payload.ConversationState.History

	if got := countStructuredToolUseTurns(hist); got != 0 {
		t.Fatalf("safe mode: expected 0 structured tool-use turns, got %d", got)
	}
	if got := countStructuredToolResultTurns(hist); got != 0 {
		t.Fatalf("safe mode: expected 0 structured tool-result turns, got %d", got)
	}
	// Tool activity must survive as narrated text so no context is lost.
	var combined string
	for _, h := range hist {
		if h.UserInputMessage != nil {
			combined += h.UserInputMessage.Content + "\n"
		}
	}
	if !containsAll(combined, "sub 1 done", "sub 2 done") {
		t.Fatalf("safe mode: expected narrated tool results, got:\n%s", combined)
	}
}

// TestKeepPairedDropsOrphanToolResults verifies that even with keepPaired=true,
// an orphaned tool_result (no matching assistant tool_use before it) is still
// flattened to text — the upstream rejects unpaired structured data.
func TestKeepPairedDropsOrphanToolResults(t *testing.T) {
	req := &ClaudeRequest{
		Model: "claude-opus-4.8",
		Messages: []ClaudeMessage{
			{Role: "user", Content: "hi"},
			// Assistant with NO tool_use, then a user tool_result → orphan.
			{Role: "assistant", Content: "sure"},
			{Role: "user", Content: []interface{}{
				map[string]interface{}{"type": "tool_result", "tool_use_id": "orphan", "content": "orphan output"},
			}},
			{Role: "user", Content: "continue"},
		},
	}
	payload := ClaudeToKiroWithHistoryMode(req, false, true)
	hist := payload.ConversationState.History

	if got := countStructuredToolResultTurns(hist); got != 0 {
		t.Fatalf("expected orphan tool result to be flattened, got %d structured result turns", got)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
