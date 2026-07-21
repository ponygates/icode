// Package permission — Bash Security Rules Engine.
//
// Claude Code 23 条安全规则的 Go 实现。覆盖所有已知的 shell 注入向量：
//   - Zsh/Bash 注入
//   - Unicode 零宽字符注入
//   - IFS 空字节注入
//   - 危险命令黑名单
//   - 路径逃逸保护
//
// 每条规则有独立的危险等级和可配置的处理方式（deny/ask/warn）。

package permission

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// BashRuleSeverity defines how severe a violation is.
type BashRuleSeverity int

const (
	// SeverityBlock — always blocked, even in YOLO mode
	SeverityBlock BashRuleSeverity = iota
	// SeverityWarn — warned in agent mode, blocked in plan mode
	SeverityWarn
	// SeverityAsk — requires user approval in agent mode
	SeverityAsk
)

// BashRule defines a single security check.
type BashRule struct {
	// ID is a unique identifier for the rule.
	ID string `yaml:"id"`
	// Description explains what this rule protects against.
	Description string `yaml:"description"`
	// Severity indicates how the rule violation is handled.
	Severity BashRuleSeverity `yaml:"severity"`
	// Match is a function that checks if the command violates this rule.
	Match func(cmd string) (matched bool, reason string)
	// Pattern is the regex pattern for serialization/display.
	Pattern string `yaml:"pattern,omitempty"`
}

// BashSecurityEngine evaluates shell commands against all known security rules.
type BashSecurityEngine struct {
	rules []BashRule
}

// NewBashSecurityEngine creates an engine with all 23+ rules loaded.
func NewBashSecurityEngine() *BashSecurityEngine {
	return &BashSecurityEngine{
		rules: defaultBashRules(),
	}
}

// Check evaluates a command against all rules. Returns the first violation found,
// or nil if the command passes all rules.
func (e *BashSecurityEngine) Check(cmd string) *BashRuleViolation {
	if cmd == "" {
		return nil
	}

	for _, rule := range e.rules {
		matched, reason := rule.Match(cmd)
		if matched {
			return &BashRuleViolation{
				RuleID:      rule.ID,
				Description: rule.Description,
				Severity:    rule.Severity,
				Reason:      reason,
			}
		}
	}
	return nil
}

// Rules returns all loaded rules.
func (e *BashSecurityEngine) Rules() []BashRule {
	return e.rules
}

// BashRuleViolation describes a security rule violation.
type BashRuleViolation struct {
	RuleID      string           `json:"rule_id"`
	Description string           `json:"description"`
	Severity    BashRuleSeverity `json:"severity"`
	Reason      string           `json:"reason"`
}

func (v *BashRuleViolation) Error() string {
	return fmt.Sprintf("[Bash-SEC-%s] %s", v.RuleID, v.Reason)
}

// ============================================================================
// 23+ Security Rules
// ============================================================================

func defaultBashRules() []BashRule {
	return []BashRule{
		// R1: Dangerous filesystem destruction
		{
			ID:          "R01",
			Description: "递归强制删除",
			Severity:    SeverityBlock,
			Pattern:     `rm\s+[-][rRfF]+|rm\s+[-][fF][rR]`,
			Match: func(cmd string) (bool, string) {
				// Check for rm -rf /, rm -rf /*, etc.
				if matched, _ := regexp.MatchString(`(?i)\brm\s+[-][rRfF]+\s+/`, cmd); matched {
					return true, "禁止递归删除根目录"
				}
				if matched, _ := regexp.MatchString(`(?i)\brm\s+[-][rRfF]+\s+~`, cmd); matched {
					return true, "禁止递归删除用户目录"
				}
				if matched, _ := regexp.MatchString(`(?i)\brm\s+[-][rRfF]+\s+\*`, cmd); matched {
					return true, "递归删除所有文件需要确认"
				}
				return false, ""
			},
		},

		// R2: Disk destruction
		{
			ID:          "R02",
			Description: "磁盘写入操作",
			Severity:    SeverityBlock,
			Pattern:     `dd\s+if=|mkfs\.|fdisk|parted|mkswap`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				dangerous := []string{
					"dd if=", "dd of=", "mkfs.", "fdisk", "parted",
					"mkswap", "dd if=/dev/zero", "dd if=/dev/urandom",
				}
				for _, d := range dangerous {
					if strings.Contains(lower, d) {
						return true, fmt.Sprintf("危险磁盘操作被禁止: %s", d)
					}
				}
				return false, ""
			},
		},

		// R3: Privilege escalation
		{
			ID:          "R03",
			Description: "权限提升",
			Severity:    SeverityBlock,
			Pattern:     `sudo|su\s+|chmod\s+777|chown\s+root`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.HasPrefix(lower, "sudo ") || strings.Contains(lower, "|sudo ") {
					return true, "禁止使用 sudo 提权"
				}
				if matched, _ := regexp.MatchString(`(?i)chmod\s+777`, cmd); matched {
					return true, "禁止 chmod 777 (所有用户完全访问)"
				}
				if matched, _ := regexp.MatchString(`(?i)chown\s+root`, cmd); matched {
					return true, "禁止更改文件所有者为 root"
				}
				return false, ""
			},
		},

		// R4: Network attacks
		{
			ID:          "R04",
			Description: "网络攻击 / 扫描",
			Severity:    SeverityBlock,
			Pattern:     `nmap|masscan|hydra|sqlmap|nikto| metasploit`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				attackTools := []string{"nmap", "masscan", "hydra", "sqlmap", "nikto", "metasploit"}
				for _, t := range attackTools {
					if strings.HasPrefix(lower, t) || strings.Contains(lower, " "+t) {
						return true, fmt.Sprintf("禁止使用网络攻击工具: %s", t)
					}
				}
				return false, ""
			},
		},

		// R5: Zsh/Bash injection via special chars
		{
			ID:          "R05",
			Description: "Shell 注入检测（特殊字符）",
			Severity:    SeverityBlock,
			Pattern:     `[;&|` + "`" + `$(){}]`,
			Match: func(cmd string) (bool, string) {
				// Allow basic pipe usage (single |) but block complex injections
				// Check for backtick command substitution
				if strings.Contains(cmd, "`") {
					return true, "禁止使用反引号命令替换"
				}
				// Check for $() command substitution (allow simple $(echo ...) but block complex)
				if strings.Contains(cmd, "$(") {
					// Allow simple cases like $(dirname ...), $(basename ...)
					simplePatterns := []string{"$(dirname", "$(basename", "$(echo", "$(pwd)", "$(which"}
					isSimple := false
					for _, p := range simplePatterns {
						if strings.Contains(cmd, p) {
							isSimple = true
							break
						}
					}
					if !isSimple {
						return true, "禁止使用复杂的命令替换 $()"
					}
				}
				// Block process substitution
				if strings.Contains(cmd, "<(") || strings.Contains(cmd, ">(") {
					return true, "禁止使用进程替换"
				}
				return false, ""
			},
		},

		// R6: Unicode zero-width characters (invisible injection)
		{
			ID:          "R06",
			Description: "Unicode 零宽字符注入检测",
			Severity:    SeverityBlock,
			Pattern:     "\\u200B|\\u200C|\\u200D|\\uFEFF|\\u2060",
			Match: func(cmd string) (bool, string) {
				zeroWidth := []rune{0x200B, 0x200C, 0x200D, 0xFEFF, 0x2060, 0x2061, 0x2062, 0x2063, 0x2064}
				for _, z := range zeroWidth {
					if strings.ContainsRune(cmd, z) {
						return true, "检测到 Unicode 零宽字符注入"
					}
				}
				return false, ""
			},
		},

		// R7: IFS null byte injection
		{
			ID:          "R07",
			Description: "IFS 空字节注入检测",
			Severity:    SeverityBlock,
			Pattern:     "\\x00|null byte",
			Match: func(cmd string) (bool, string) {
				if strings.Contains(cmd, "\x00") {
					return true, "检测到空字节注入"
				}
				return false, ""
			},
		},

		// R8: System configuration modification
		{
			ID:          "R08",
			Description: "系统配置修改",
			Severity:    SeverityBlock,
			Pattern:     `>/etc/|>/sys/|>/proc/|>/boot/|>/dev/sd`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				systemPaths := []string{
					">/etc/", ">/sys/", ">/proc/",
					">/boot/", ">/dev/sda", ">/dev/nvme",
					"|tee /etc/", ">>/etc/",
				}
				for _, p := range systemPaths {
					if strings.Contains(lower, p) {
						return true, "禁止修改系统配置文件"
					}
				}
				return false, ""
			},
		},

		// R9: Package manager dangerous operations
		{
			ID:          "R09",
			Description: "包管理器危险操作",
			Severity:    SeverityAsk,
			Pattern:     `npm\s+publish|pip\s+install\s+--user|gem\s+install|cargo\s+publish`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.Contains(lower, "npm publish") || strings.Contains(lower, "npm unpublish") {
					return true, "发布 npm 包需要谨慎确认"
				}
				// Warn on global installs
				if matched, _ := regexp.MatchString(`(?i)(npm|pip|gem)\s+install\s+-g\s`, cmd); matched {
					return true, "全局安装包需要确认"
				}
				return false, ""
			},
		},

		// R10: Git operations on protected branches
		{
			ID:          "R10",
			Description: "Git 保护分支操作",
			Severity:    SeverityAsk,
			Pattern:     `git\s+push\s+.*\s+master|git\s+push\s+.*\s+main\s+.*-f|git\s+push\s+--force`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if strings.Contains(lower, "git push --force") || strings.Contains(lower, "git push -f") {
					return true, "强制推送 git 需要确认"
				}
				if strings.Contains(lower, "git reset --hard") {
					return true, "git reset --hard 会丢失未提交的更改，需要确认"
				}
				return false, ""
			},
		},

		// R11: File permission changes
		{
			ID:          "R11",
			Description: "文件权限变更",
			Severity:    SeverityAsk,
			Pattern:     `chmod|chown|setfacl|getfacl`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.HasPrefix(lower, "chmod ") || strings.HasPrefix(lower, "chown ") {
					// Allow basic chmod +x on files
					if matched, _ := regexp.MatchString(`(?i)chmod\s+[+-]x\s`, cmd); matched {
						return false, ""
					}
					return true, "修改文件权限需要确认"
				}
				return false, ""
			},
		},

		// R12: Environment variable leaks
		{
			ID:          "R12",
			Description: "环境变量泄露检测",
			Severity:    SeverityBlock,
			Pattern:     `echo\s+\$[A-Z_]*KEY|env|printenv|export\s+.*KEY`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				// Check for printing sensitive env vars
				sensitiveVars := []string{"api_key", "api_secret", "password", "token", "secret", "key", "auth"}
				for _, v := range sensitiveVars {
					patterns := []string{
						fmt.Sprintf("echo $%s", v),
						fmt.Sprintf("echo ${%s", v),
						fmt.Sprintf("export %s", v),
					}
					for _, p := range patterns {
						if strings.Contains(lower, p) {
							return true, fmt.Sprintf("禁止泄露敏感环境变量: %s", v)
						}
					}
				}
				return false, ""
			},
		},

		// R13: curl/wget to internal networks
		{
			ID:          "R13",
			Description: "内网地址请求",
			Severity:    SeverityAsk,
			Pattern:     `curl\s+.*\b10\.|curl\s+.*\b172\.|curl\s+.*\b192\.168`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if !strings.HasPrefix(lower, "curl ") && !strings.HasPrefix(lower, "wget ") {
					return false, ""
				}
				// Check for internal IPs
				privateIPs := []string{"10.", "172.16.", "172.17.", "172.18.", "172.19.",
					"172.20.", "172.21.", "172.22.", "172.23.",
					"172.24.", "172.25.", "172.26.", "172.27.",
					"172.28.", "172.29.", "172.30.", "172.31.",
					"192.168.", "127.0.0.1", "localhost"}
				for _, ip := range privateIPs {
					if strings.Contains(lower, ip) {
						return true, "请求内网地址需要确认"
					}
				}
				return false, ""
			},
		},

		// R14: Crypto/blockchain operations
		{
			ID:          "R14",
			Description: "加密/区块链操作",
			Severity:    SeverityBlock,
			Pattern:     `openssl\s+enc|gpg\s+--symmetric|gpg\s+--encrypt|ssh-keygen`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				dangerous := []string{
					"openssl enc -d", "gpg --decrypt", "gpg --symmetric",
					"gpg --encrypt", "gpg --sign",
				}
				for _, d := range dangerous {
					if strings.Contains(lower, d) {
						return true, "加密/解密操作需要确认"
					}
				}

				// Allow openssl for simple operations (e.g., random bytes)
				return false, ""
			},
		},

		// R15: Docker container escape
		{
			ID:          "R15",
			Description: "Docker 逃逸操作",
			Severity:    SeverityBlock,
			Pattern:     `docker\s+run\s+.*--privileged|docker\s+exec\s+-it|docker\s+run\s+.*-v\s+/:/`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.Contains(lower, "--privileged") && strings.Contains(lower, "docker") {
					return true, "禁止 Docker 特权模式"
				}
				if strings.Contains(lower, "-v /:/") || strings.Contains(lower, "--volume /:/") {
					return true, "禁止将根目录挂载到容器"
				}
				return false, ""
			},
		},

		// R16: SSH key operations
		{
			ID:          "R16",
			Description: "SSH 密钥操作",
			Severity:    SeverityAsk,
			Pattern:     `ssh-keygen|ssh-copy-id|scp|rsync\s+-e\s+ssh`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.Contains(lower, "ssh-copy-id") {
					return true, "复制 SSH 公钥到远程主机需要确认"
				}
				if strings.Contains(lower, "ssh-keygen") && !strings.Contains(lower, "-y") {
					return true, "生成 SSH 密钥需要确认"
				}
				return false, ""
			},
		},

		// R17: Shell configuration modification
		{
			ID:          "R17",
			Description: "Shell 配置文件修改",
			Severity:    SeverityAsk,
			Pattern:     `~/.bashrc|~/.zshrc|~/.profile|~/.bash_profile|/etc/profile`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				shellConfigs := []string{".bashrc", ".zshrc", ".profile", ".bash_profile"}
				// Only check if this is a write operation (>>, >, tee)
				if strings.Contains(lower, ">>") || strings.Contains(lower, "> ") || strings.Contains(lower, "tee") {
					for _, cfg := range shellConfigs {
						if strings.Contains(lower, cfg) {
							return true, "修改 shell 配置文件需要确认"
						}
					}
				}
				return false, ""
			},
		},

		// R18: .git directory manipulation
		{
			ID:          "R18",
			Description: ".git 目录操作保护",
			Severity:    SeverityBlock,
			Pattern:     `rm\s+.*\.git|rmdir\s+.*\.git|mv\s+.*\.git`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if strings.Contains(lower, ".git") {
					// Only block destructive operations on .git
					destructivePatterns := []string{
						"rm ", "rmdir ", "mv ", "chmod ", "chown ",
					}
					for _, p := range destructivePatterns {
						if strings.Contains(lower, p) && strings.Contains(lower, ".git") {
							return true, "禁止修改 .git 目录"
						}
					}
				}
				return false, ""
			},
		},

		// R19: .icode directory protection (like .claude protection)
		{
			ID:          "R19",
			Description: ".icode 配置目录保护",
			Severity:    SeverityBlock,
			Pattern:     `rm\s+.*\.icode|rmdir\s+.*\.icode`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if strings.Contains(lower, ".icode") && (strings.Contains(lower, "rm ") || strings.Contains(lower, "rmdir ")) {
					return true, "禁止删除 .icode 配置目录"
				}
				return false, ""
			},
		},

		// R20: curl piping to shell (classic attack vector)
		{
			ID:          "R20",
			Description: "curl|bash 注入检测",
			Severity:    SeverityBlock,
			Pattern:     `curl\s+.*\|.*sh|wget\s+.*\|.*sh`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				// curl url | sh, curl url | bash
				if matched, _ := regexp.MatchString(`(?i)(curl|wget)\s+[^\|]+\|\s*(bash|sh|zsh|fish)`, lower); matched {
					return true, "禁止从 URL 直接 pipe 到 shell（curl|sh 攻击向量）"
				}
				return false, ""
			},
		},

		// R21: Base64 decode and pipe to shell
		{
			ID:          "R21",
			Description: "Base64 解码注入检测",
			Severity:    SeverityBlock,
			Pattern:     `base64\s+-d.*\|.*sh|echo\s+[A-Za-z0-9+/=]+\s*\|.*base64.*\|.*sh`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if strings.Contains(lower, "base64 -d") || strings.Contains(lower, "base64 --decode") {
					if strings.Contains(lower, "| sh") || strings.Contains(lower, "| bash") {
						return true, "禁止 base64 解码后执行 shell"
					}
				}
				return false, ""
			},
		},

		// R22: wget/curl to external URL that downloads executables
		{
			ID:          "R22",
			Description: "下载并执行二进制文件",
			Severity:    SeverityAsk,
			Pattern:     `curl\s+-o|wget\s+-O|curl\s+.*\.(exe|msi|sh|bin)`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(cmd)
				if strings.HasPrefix(lower, "curl -o ") || strings.HasPrefix(lower, "wget -o ") {
					return true, "下载文件到本地需要确认"
				}
				if strings.Contains(lower, "chmod +x") && (strings.Contains(lower, "curl") || strings.Contains(lower, "wget")) {
					return true, "下载并赋予执行权限需要确认"
				}
				return false, ""
			},
		},

		// R23: Path traversal / working directory escape
		{
			ID:          "R23",
			Description: "路径逃逸检测",
			Severity:    SeverityBlock,
			Pattern:     `\.\./\.\./|~\.\./`,
			Match: func(cmd string) (bool, string) {
				// Only check for extreme path traversal in write/delete operations
				if strings.Contains(cmd, "../") {
					// Check for deep traversal (more than 2 levels)
					count := strings.Count(cmd, "../")
					absCmd := cmd
					absCmd = strings.ReplaceAll(absCmd, " ", "")
					if count >= 3 {
						return true, "深层路径遍历被禁止"
					}
				}
				return false, ""
			},
		},

		// R-RTKn: Rust-specific (cargo audit, publish)
		{
			ID:          "R-RTKn",
			Description: "Rust 包管理操作",
			Severity:    SeverityAsk,
			Pattern:     `cargo\s+publish|cargo\s+owner|cargo\s+yank|cargo\s+login`,
			Match: func(cmd string) (bool, string) {
				lower := strings.ToLower(strings.TrimSpace(cmd))
				if strings.HasPrefix(lower, "cargo publish") {
					return true, "发布 crate 需要确认"
				}
				return false, ""
			},
		},
	}
}

// ============================================================================
// Integration helpers
// ============================================================================

// IsDeniedBashCommand checks a bash command against all rules and returns
// true if it's unconditionally blocked (SeverityBlock).
func IsDeniedBashCommand(cmd string) *BashRuleViolation {
	engine := NewBashSecurityEngine()
	violation := engine.Check(cmd)
	if violation != nil && violation.Severity == SeverityBlock {
		return violation
	}
	return nil
}

// CheckBashCommand runs the full rule engine and returns all violations found.
// Use this for the enhanced permission gate.
func CheckBashCommand(cmd string) []BashRuleViolation {
	engine := NewBashSecurityEngine()
	var violations []BashRuleViolation
	for _, rule := range engine.rules {
		matched, reason := rule.Match(cmd)
		if matched {
			violations = append(violations, BashRuleViolation{
				RuleID:      rule.ID,
				Description: rule.Description,
				Severity:    rule.Severity,
				Reason:      reason,
			})
		}
	}
	return violations
}

// validatePathForBash checks if a file path is safe for bash operations.
// Returns nil if safe, or a violation if the path is dangerous.
func validatePathForBash(path string) error {
	if path == "" {
		return nil
	}

	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("invalid path: %w", err)
	}

	// Block operations on critical system paths
	criticalPaths := []string{
		"/etc", "/sys", "/proc", "/boot", "/dev",
		"/usr", "/bin", "/sbin", "/lib",
		`C:\Windows`, `C:\Program Files`, `C:\ProgramData`,
		`C:\System32`, `C:\Boot`,
	}

	lower := strings.ToLower(absPath)
	for _, cp := range criticalPaths {
		if strings.HasPrefix(lower, strings.ToLower(cp)) {
			return fmt.Errorf("禁止操作关键系统路径: %s", cp)
		}
	}

	return nil
}
