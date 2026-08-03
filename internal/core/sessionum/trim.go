package sessionum

import (
	"github.com/ponygates/icode/internal/types"
)

// TrimToBudget keeps the newest messages whose combined estimated size fits
// within budget, leaving ~30% headroom for the new input and the completion.
// It returns the subset (a slice of the original) and whether anything was
// dropped. Pure estimation — the real provider count runs later in tokenopt.
func TrimToBudget(msgs []types.Message, budget int) ([]types.Message, bool) {
	if budget <= 0 || len(msgs) == 0 {
		return msgs, false
	}
	capTokens := budget * 70 / 100
	if capTokens <= 0 {
		capTokens = budget
	}
	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		t := approxTokens(msgs[i])
		if total+t > capTokens {
			return msgs[i:], i > 0
		}
		total += t
	}
	return msgs, false
}

// approxTokens is a cheap, provider-agnostic token estimate (≈1 token per 3
// runes, plus a flat allowance per attachment). Close enough for a soft guard.
func approxTokens(m types.Message) int {
	n := len([]rune(m.Content))/3 + 1
	for range m.Attachments {
		n += 200
	}
	return n
}
