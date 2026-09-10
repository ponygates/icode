package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ponygates/icode/internal/types"
)

// llmClassifier is the auto-mode permission classifier (Claude Code parity):
// a cheap model evaluates a tool call and decides whether it is safe to
// auto-approve, so auto mode only interrupts the user for genuinely risky
// operations. Implements permission.Classifier.
type llmClassifier struct {
	prov  types.Provider
	model string
}

// classifyPrompt asks for a strict JSON verdict on one tool call.
const classifyPrompt = `你是终端 AI 编码助手的工具调用安全审查器。判断这次工具调用是否可以"自动批准"（无需打扰用户）。
自动批准的标准：操作对本地数据安全、不越权、无不可逆副作用、不访问敏感信息。
只要有任何风险（删除/覆盖数据、执行不可逆命令、写入系统目录、访问凭据、外发敏感内容等），就应判为不批准。
工具: %s
参数: %s
只输出一行 JSON：{"allow": true 或 false, "reason": "一句话中文理由"}`

// Classify evaluates one tool call. Returns allow + a short Chinese reason.
func (c *llmClassifier) Classify(ctx context.Context, toolName, toolInput string) (bool, string, error) {
	prompt := fmt.Sprintf(classifyPrompt, toolName, truncateStr(toolInput, 800))
	msg, err := c.prov.Chat(ctx, types.ChatRequest{
		Model:     c.model,
		Messages:  []types.Message{{Role: types.RoleUser, Content: prompt}},
		MaxTokens: 60,
		// nil = leave the provider default in place. This used to read
		// `0`, which the old `> 0` guard dropped from the wire anyway —
		// nil states that intent instead of relying on the omission.
		Temperature: nil,
	})
	if err != nil {
		return false, "", fmt.Errorf("classifier: %w", err)
	}
	content := strings.TrimSpace(msg.Content)
	// Tolerate code-fence / trailing text around the JSON object.
	if i := strings.Index(content, "{"); i >= 0 {
		if j := strings.LastIndex(content, "}"); j > i {
			content = content[i : j+1]
		}
	}
	var out struct {
		Allow  bool   `json:"allow"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(content), &out); err != nil {
		return false, "", fmt.Errorf("classifier: bad JSON %q: %w", truncateStr(content, 120), err)
	}
	return out.Allow, out.Reason, nil
}

// SetClassifierModel wires the auto-mode classifier into the permission gate.
// modelID uses "provider/model" (e.g. "deepseek/deepseek-chat"); empty or an
// unresolvable provider leaves auto mode rule-based. Best-effort: a bad model
// degrades gracefully (auto keeps asking) instead of breaking the gate.
func (e *Engine) SetClassifierModel(modelID string) {
	e.mu.Lock()
	gate := e.gate
	provReg := e.providerReg
	e.cheapModelID = strings.TrimSpace(modelID)
	e.mu.Unlock()
	if gate == nil || provReg == nil {
		return
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		gate.SetClassifier(nil)
		return
	}
	provName, model, ok := splitProviderModel(modelID)
	if !ok {
		return
	}
	prov, err := provReg.Get(provName)
	if err != nil {
		return
	}
	gate.SetClassifier(&llmClassifier{prov: prov, model: model})
}

// GenerateCommitMessage drafts a one-line commit message from a unified diff
// using the configured cheap model (the same one as the auto-mode classifier).
// Used by the desktop git workbench. Returns an error when no cheap model is
// configured so the UI can hide the affordance instead of guessing.
func (e *Engine) GenerateCommitMessage(ctx context.Context, diff string) (string, error) {
	e.mu.Lock()
	modelID := e.cheapModelID
	provReg := e.providerReg
	e.mu.Unlock()
	if modelID == "" {
		return "", fmt.Errorf("no classifier_model configured")
	}
	provName, model, ok := splitProviderModel(modelID)
	if !ok {
		return "", fmt.Errorf("bad classifier_model %q", modelID)
	}
	prov, err := provReg.Get(provName)
	if err != nil {
		return "", fmt.Errorf("provider %q: %w", provName, err)
	}
	prompt := fmt.Sprintf(commitMsgPrompt, truncateStr(diff, 6000))
	msg, err := prov.Chat(ctx, types.ChatRequest{
		Model:     model,
		Messages:  []types.Message{{Role: types.RoleUser, Content: prompt}},
		MaxTokens: 80,
		// nil = leave the provider default in place (see the classifier above).
		Temperature: nil,
	})
	if err != nil {
		return "", fmt.Errorf("commit message: %w", err)
	}
	out := strings.TrimSpace(msg.Content)
	// Strip markdown fences / surrounding quotes some models add.
	out = strings.Trim(out, "`\"' \n")
	// First line only — the workbench input is single-line by design.
	if i := strings.IndexAny(out, "\r\n"); i >= 0 {
		out = strings.TrimSpace(out[:i])
	}
	if len([]rune(out)) > 100 {
		out = string([]rune(out)[:100]) + "…"
	}
	return out, nil
}

const commitMsgPrompt = `根据下面的 git diff 草拟一条简洁的提交信息。要求：中文或英文与 diff 注释语言一致；格式为 conventional commit（feat:/fix:/refactor:/docs:/chore: 之一 + 一句话描述，不超过 50 字）；不要解释，只输出提交信息本身。
diff:
%s`

// splitProviderModel parses "provider/model" into its parts.
func splitProviderModel(id string) (string, string, bool) {
	i := strings.Index(id, "/")
	if i <= 0 || i == len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

func truncateStr(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
