const fs = require('fs');
const must = (c, m) => { if (!c) { console.error('FAIL ' + m); process.exit(1); } };
let s = fs.readFileSync('internal/tui/raw_input.go', 'utf8');

// 1. ↑ recall → into inputBuf (raw-string safe via String.raw)
const a1 = String.raw`									if hasQueue {
										t.queueBuf = t.queue[0]
										t.queue = t.queue[1:]
									}
									t.mu.Unlock()
									if hasQueue {
										t.render()
									}
								}`;
must(s.includes(a1), 'recall');
s = s.replace(a1, String.raw`									hasQueue := len(t.queue) > 0
									if hasQueue {
										recalled := t.queue[0]
										t.queue = t.queue[1:]
										if t.inputBuf != "" {
											recalled = t.inputBuf + " " + recalled
										}
										t.inputBuf = recalled
										t.cursor = len([]rune(t.inputBuf))
									}
									t.mu.Unlock()
									if hasQueue {
										t.updateSuggestions()
										t.render()
									}
								}`);

// 2. handleQueueKey rework
const a2 = String.raw`// handleQueueKey maintains the streaming-time input buffer + queue.
func (t *TUI) handleQueueKey(r rune) {
	switch r {
	case '\r':
		if txt := strings.TrimSpace(t.queueBuf); txt != "" {
			t.queue = append(t.queue, txt)
		}
		t.queueBuf = ""
		t.render()
	case 0x7f, 0x08: // Backspace
		if runes := []rune(t.queueBuf); len(runes) > 0 {
			t.queueBuf = string(runes[:len(runes)-1])
			t.render()
		}
	default:
		if r >= 0x20 && r != 0x7f {
			t.queueBuf += string(r)
			t.render()
		}
	}
}`;
must(s.includes(a2), 'hqk');
s = s.replace(a2, String.raw`// handleQueueKey handles typing while the agent is streaming. Claude Code
// UX: characters go straight into the REGULAR input box (fully editable —
// cursor moves and backspace work), and Enter QUEUES the current input as
// the next turn. The old parallel "queue buffer" (a ⏳ line above the input
// mirroring every keystroke) made users think their text was landing in the
// wrong place.
func (t *TUI) handleQueueKey(r rune) {
	switch r {
	case '\r':
		t.mu.Lock()
		txt := strings.TrimSpace(t.inputBuf)
		if txt != "" {
			t.queue = append(t.queue, txt)
			t.inputBuf = ""
			t.cursor = 0
		}
		t.mu.Unlock()
		t.updateSuggestions()
		t.render()
	case 0x7f, 0x08: // Backspace
		t.deleteAtCursor()
		t.updateSuggestions()
		t.render()
	default:
		if r >= 0x20 && r != 0x7f {
			t.mu.Lock()
			runes := []rune(t.inputBuf)
			if t.cursor > len(runes) {
				t.cursor = len(runes)
			}
			rest := append([]rune{r}, runes[t.cursor:]...)
			runes = append(runes[:t.cursor], rest...)
			t.inputBuf = string(runes)
			t.cursor++
			t.mu.Unlock()
			t.updateSuggestions()
			t.render()
		}
	}
}`);

// 3. queueLine: drop the "输入中" variant (queueBuf retired)
const a3 = String.raw`	if t.queueBuf != "" {
		return t.paint("dim", "⏳ 输入中: "+t.queueBuf+" （Enter 排队 · ↑ 取回）")
	}
	if len(t.queue) > 0 {`;
must(s.includes(a3), 'qline — wrong file?');
console.log('unexpected qline in raw_input');
fs.writeFileSync('internal/tui/raw_input.go', s, 'utf8');
console.log('part1 OK');
