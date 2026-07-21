// Package tokenopt — Snip filter integration with Optimizer.
//
// Snip is a zero-cost filter that removes useless conversation turns
// before they consume context budget. It implements Claude Code's Level 0
// compression — the cheapest layer in the 5-layer pipeline.
//
// Integration points:
//   - New() initializes the snip filter from Config.Snip
//   - SetSnipConfig() allows runtime reconfiguration
//   - CompactRequest() filters messages before sending to the LLM
//   - estimateTokensLocked() filters before counting

package tokenopt

import "github.com/ponygates/icode/internal/types"

// SetSnipConfig overrides the default snip filter configuration.
func (o *Optimizer) SetSnipConfig(cfg SnipConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.snipConfig = cfg
	o.snipFilter = NewSnipFilter(cfg)
}

// SnipConfig returns the current snip filter configuration.
func (o *Optimizer) SnipConfig() SnipConfig {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.snipConfig
}

// filterMessages applies the snip filter to a message slice.
// Returns the original slice if no filter is configured.
func (o *Optimizer) filterMessages(msgs []types.Message) []types.Message {
	if o.snipFilter == nil {
		return msgs
	}
	return o.snipFilter.Filter(msgs)
}
