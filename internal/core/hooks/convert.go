package hooks

import "github.com/ponygates/icode/internal/config"

// RulesFromConfig converts the config-layer hook rules (config.Hooks) into
// the runtime Rule map consumed by NewRunner. Single conversion point for
// every injection site (app startup, cmd onConfigChanged refresh, slashui
// /hooks live-reload) so the three stay in lockstep.
func RulesFromConfig(hooksCfg map[string][]config.HookRule) map[string][]Rule {
	if len(hooksCfg) == 0 {
		return map[string][]Rule{}
	}
	rules := make(map[string][]Rule, len(hooksCfg))
	for ev, list := range hooksCfg {
		for _, hr := range list {
			rules[ev] = append(rules[ev], Rule{Matcher: hr.Matcher, Command: hr.Command, Timeout: hr.Timeout})
		}
	}
	return rules
}
