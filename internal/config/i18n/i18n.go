// Package i18n provides internationalization with support for zh-CN, zh-TW, and en.
package i18n

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// Lang represents a supported locale.
type Lang string

const (
	ZhCN Lang = "zh-CN"
	ZhTW Lang = "zh-TW"
	En   Lang = "en"
)

// T is the global translator singleton.
var T = &Translator{
	current: ZhCN,
	strings: make(map[Lang]map[string]string),
}

// Translator holds string maps for each locale.
type Translator struct {
	mu       sync.RWMutex
	current  Lang
	strings  map[Lang]map[string]string
	fallback Lang
}

// SetLanguage switches the active locale.
func (t *Translator) SetLanguage(l Lang) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.current = l
	t.fallback = En
}

// Language returns the current locale.
func (t *Translator) Language() Lang {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.current
}

// T (translate) looks up a key in the current locale, falling back to English.
func (t *Translator) T(key string, args ...any) string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	lang := t.current

	s := t.lookup(lang, key)
	if s == "" && t.fallback != "" {
		s = t.lookup(t.fallback, key)
	}
	if s == "" {
		s = key
	}

	if len(args) > 0 {
		s = fmt.Sprintf(s, args...)
	}
	return s
}

func (t *Translator) lookup(l Lang, key string) string {
	if m, ok := t.strings[l]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	return ""
}

// LoadDir walks a directory and loads all .json and .yaml translation files.
// Files should be named: zh-CN.json, zh-TW.json, en.json
func (t *Translator) LoadDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("read i18n dir: %w", err)
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		lang := Lang(name[:len(name)-len(ext)])

		switch lang {
		case ZhCN, ZhTW, En:
		default:
			continue
		}

		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}

		m := make(map[string]string)
		switch ext {
		case ".json":
			if err := json.Unmarshal(data, &m); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		case ".yaml", ".yml":
			if err := yaml.Unmarshal(data, &m); err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
		}

		t.strings[lang] = m
	}

	return nil
}

// Tr is a convenience function for the global translator.
func Tr(key string, args ...any) string {
	return T.T(key, args...)
}

// ============================================================================
// Embedded fallback strings — loaded if no configs/i18n/ directory exists
// ============================================================================

var embeddedStrings = map[Lang]map[string]string{
	ZhCN: {
		"app.name":        "iCode",
		"app.tagline":     "多模型 AI 编程 Agent",
		"cli.welcome":     "欢迎使用 iCode！输入 /help 查看命令列表。",
		"cli.goodbye":     "再见！",
		"cmd.chat.desc":   "启动交互式编程对话",
		"cmd.exec.desc":   "执行单次 prompt（非交互模式）",
		"cmd.auth.desc":   "配置 Provider API 密钥",
		"model.select":    "选择模型",
		"model.provider":  "提供商",
		"tool.bash.desc":  "在沙箱中执行 Shell 命令",
		"tool.read.desc":  "读取文件内容",
		"tool.write.desc": "写入文件",
		"tool.grep.desc":  "在项目中搜索文本",
		"tool.glob.desc":  "按模式查找文件",
		"perm.ask":        "是否允许执行此操作？",
		"perm.allow":      "允许",
		"perm.deny":       "拒绝",
		"perm.allow_all":  "全部允许",
		"token.usage":     "Token 用量",
		"token.input":     "输入",
		"token.output":    "输出",
		"token.cache_hit": "缓存命中",
		"token.cost":      "预估费用",
		"session.list":    "会话列表",
		"session.new":     "新建会话",
		"session.delete":  "删除会话",
		"update.checking": "正在检查模型更新...",
		"update.updated":  "模型列表已更新",
		"error.unknown":   "发生未知错误",
		"lang.changed":    "语言已切换为 %s",
	},
	ZhTW: {
		"app.name":        "iCode",
		"app.tagline":     "多模型 AI 程式開發 Agent",
		"cli.welcome":     "歡迎使用 iCode！輸入 /help 檢視指令列表。",
		"cli.goodbye":     "再見！",
		"cmd.chat.desc":   "啟動互動式程式開發對話",
		"cmd.exec.desc":   "執行單次 prompt（非互動模式）",
		"cmd.auth.desc":   "設定 Provider API 金鑰",
		"model.select":    "選擇模型",
		"model.provider":  "提供者",
		"tool.bash.desc":  "在沙箱中執行 Shell 指令",
		"tool.read.desc":  "讀取檔案內容",
		"tool.write.desc": "寫入檔案",
		"tool.grep.desc":  "在專案中搜尋文字",
		"tool.glob.desc":  "按模式尋找檔案",
		"perm.ask":        "是否允許執行此操作？",
		"perm.allow":      "允許",
		"perm.deny":       "拒絕",
		"perm.allow_all":  "全部允許",
		"token.usage":     "Token 用量",
		"token.input":     "輸入",
		"token.output":    "輸出",
		"token.cache_hit": "快取命中",
		"token.cost":      "預估費用",
		"session.list":    "對話列表",
		"session.new":     "新增對話",
		"session.delete":  "刪除對話",
		"update.checking": "正在檢查模型更新...",
		"update.updated":  "模型列表已更新",
		"error.unknown":   "發生未知錯誤",
		"lang.changed":    "語言已切換為 %s",
	},
	En: {
		"app.name":        "iCode",
		"app.tagline":     "Multi-Model AI Coding Agent",
		"cli.welcome":     "Welcome to iCode! Type /help to see available commands.",
		"cli.goodbye":     "Goodbye!",
		"cmd.chat.desc":   "Start an interactive coding session",
		"cmd.exec.desc":   "Execute a single prompt (non-interactive)",
		"cmd.auth.desc":   "Configure provider API keys",
		"model.select":    "Select Model",
		"model.provider":  "Provider",
		"tool.bash.desc":  "Execute shell commands in a sandbox",
		"tool.read.desc":  "Read file contents",
		"tool.write.desc": "Write to a file",
		"tool.grep.desc":  "Search text in project",
		"tool.glob.desc":  "Find files by pattern",
		"perm.ask":        "Allow this operation?",
		"perm.allow":      "Allow",
		"perm.deny":       "Deny",
		"perm.allow_all":  "Allow All",
		"token.usage":     "Token Usage",
		"token.input":     "Input",
		"token.output":    "Output",
		"token.cache_hit": "Cache Hit",
		"token.cost":      "Est. Cost",
		"session.list":    "Sessions",
		"session.new":     "New Session",
		"session.delete":  "Delete Session",
		"update.checking": "Checking for model updates...",
		"update.updated":  "Model list updated",
		"error.unknown":   "An unknown error occurred",
		"lang.changed":    "Language switched to %s",
	},
}

func init() {
	T.strings = embeddedStrings
}
