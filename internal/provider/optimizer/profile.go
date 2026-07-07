package optimizer

import (
	"fmt"
	"strings"
)

type Profile struct {
	Provider string
	Model    string

	Temperature float64
	TopP        float64
	MaxTokens   int

	ToolStyle string

	SystemPrompt string

	StripThinkTag bool
	MaxOutputLines int

	EnableThinking bool
}

type MatchRule struct {
	ProviderPrefix string
	ModelContains  string
	Profile        Profile
}

var profiles = []MatchRule{
	{ProviderPrefix: "deepseek", Profile: deepseekProfile},
	{ProviderPrefix: "openai", ModelContains: "o3", Profile: openaiOProfile},
	{ProviderPrefix: "openai", ModelContains: "o4", Profile: openaiOProfile},
	{ProviderPrefix: "openai", Profile: openaiProfile},
	{ProviderPrefix: "anthropic", ModelContains: "opus", Profile: claudeOpusProfile},
	{ProviderPrefix: "anthropic", ModelContains: "sonnet", Profile: claudeSonnetProfile},
	{ProviderPrefix: "anthropic", Profile: claudeDefaultProfile},
	{ProviderPrefix: "zhipu", ModelContains: "flash", Profile: glmFlashProfile},
	{ProviderPrefix: "zhipu", Profile: glmProfile},
	{ProviderPrefix: "siliconflow", Profile: siliconflowProfile},
	{ProviderPrefix: "nvidia", Profile: nvidiaProfile},
	{ProviderPrefix: "openrouter", Profile: openrouterProfile},
	{ProviderPrefix: "qwen", Profile: qwenProfile},
	{ProviderPrefix: "doubao", Profile: doubaoProfile},
	{ProviderPrefix: "hunyuan", Profile: hunyuanProfile},
	{ProviderPrefix: "ernie", Profile: ernieProfile},
	{ProviderPrefix: "spark", Profile: sparkProfile},
	{ProviderPrefix: "minimax", Profile: minimaxProfile},
	{ProviderPrefix: "baichuan", Profile: baichuanProfile},
	{ProviderPrefix: "yi", Profile: yiProfile},
	{ProviderPrefix: "step", Profile: stepProfile},
	{ProviderPrefix: "kimi", Profile: kimiProfile},
	{ProviderPrefix: "pangu", Profile: panguProfile},
}

func ForProvider(providerName, modelName string) Profile {
	for _, rule := range profiles {
		if !strings.HasPrefix(providerName, rule.ProviderPrefix) {
			continue
		}
		if rule.ModelContains != "" && !strings.Contains(modelName, rule.ModelContains) {
			continue
		}
		p := rule.Profile
		p.Provider = providerName
		p.Model = modelName
		if p.SystemPrompt == "" {
			p.SystemPrompt = defaultSystemPrompt(providerName)
		}
		return p
	}

	return Profile{
		Provider:    providerName,
		Model:       modelName,
		Temperature: 0.3,
		TopP:        0.9,
		MaxTokens:   4096,
		ToolStyle:   "openai",
		SystemPrompt: defaultSystemPrompt(providerName),
	}
}

func (p Profile) DisplayName() string {
	return fmt.Sprintf("%s/%s", p.Provider, p.Model)
}

func defaultSystemPrompt(provider string) string {
	return fmt.Sprintf(`You are iCode, an AI-native coding assistant powered by %s.

You help users write, understand, and modify code. You have access to tools that let you read files, write files, execute shell commands, and search code.

When working:
1. First understand what the user wants by reading relevant files
2. Make targeted changes using the edit tool when possible
3. Run build/test commands to verify your changes
4. Follow existing code conventions and patterns
5. Be concise — show only what matters

Available tools are listed below. Use them as needed.`, provider)
}
