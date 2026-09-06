const fs = require('fs');
const must = (c, m) => { if (!c) { console.error('FAIL ' + m); process.exit(1); } };
let s = fs.readFileSync('internal/tui/raw_input.go', 'utf8');
const T = '\t';

// 1. ↑ recall → into inputBuf (exact 8-tab indentation)
const a1 = [
  T.repeat(8) + 'if hasQueue {',
  T.repeat(9) + 't.queueBuf = t.queue[0]',
  T.repeat(9) + 't.queue = t.queue[1:]',
  T.repeat(8) + '}',
  T.repeat(8) + 't.mu.Unlock()',
  T.repeat(8) + 'if hasQueue {',
  T.repeat(9) + 't.render()',
  T.repeat(8) + '}',
].join('\n');
must(s.includes(a1), 'recall');
const n1 = [
  T.repeat(8) + 'hasQueue := len(t.queue) > 0',
  T.repeat(8) + 'if hasQueue {',
  T.repeat(9) + 'recalled := t.queue[0]',
  T.repeat(9) + 't.queue = t.queue[1:]',
  T.repeat(9) + 'if t.inputBuf != "" {',
  T.repeat(10) + 'recalled = t.inputBuf + " " + recalled',
  T.repeat(9) + '}',
  T.repeat(9) + 't.inputBuf = recalled',
  T.repeat(9) + 't.cursor = len([]rune(t.inputBuf))',
  T.repeat(8) + '}',
  T.repeat(8) + 't.mu.Unlock()',
  T.repeat(8) + 'if hasQueue {',
  T.repeat(9) + 't.updateSuggestions()',
  T.repeat(9) + 't.render()',
  T.repeat(8) + '}',
].join('\n');
s = s.replace(a1, n1);

// 2. handleQueueKey rework (exact text from dump)
const a2 = [
  T + 'switch r {',
  T + `case '\r':`,
  T.repeat(2) + 'if txt := strings.TrimSpace(t.queueBuf); txt != "" {',
  T.repeat(3) + 't.queue = append(t.queue, txt)',
  T.repeat(2) + '}',
  T.repeat(2) + 't.queueBuf = ""',
  T.repeat(2) + 't.render()',
  T + 'case 0x7f, 0x08: // Backspace',
  T.repeat(2) + 'if runes := []rune(t.queueBuf); len(runes) > 0 {',
  T.repeat(3) + 't.queueBuf = string(runes[:len(runes)-1])',
  T.repeat(2) + 't.render()',
  T.repeat(2) + '}',
  T + 'default:',
  T.repeat(2) + 'if r >= 0x20 && r != 0x7f {',
  T.repeat(3) + 't.queueBuf += string(r)',
  T.repeat(3) + 't.render()',
  T.repeat(2) + '}',
  T + '}',
].join('\n');
must(s.includes(a2), 'hqk');
const n2 = [
  T + 'switch r {',
  T + `case '\r':`,
  T.repeat(2) + 't.mu.Lock()',
  T.repeat(2) + 'txt := strings.TrimSpace(t.inputBuf)',
  T.repeat(2) + 'if txt != "" {',
  T.repeat(3) + 't.queue = append(t.queue, txt)',
  T.repeat(3) + 't.inputBuf = ""',
  T.repeat(3) + 't.cursor = 0',
  T.repeat(2) + '}',
  T.repeat(2) + 't.mu.Unlock()',
  T.repeat(2) + 't.updateSuggestions()',
  T.repeat(2) + 't.render()',
  T + 'case 0x7f, 0x08: // Backspace',
  T.repeat(2) + 't.deleteAtCursor()',
  T.repeat(2) + 't.updateSuggestions()',
  T.repeat(2) + 't.render()',
  T + 'default:',
  T.repeat(2) + 'if r >= 0x20 && r != 0x7f {',
  T.repeat(3) + 't.mu.Lock()',
  T.repeat(3) + 'runes := []rune(t.inputBuf)',
  T.repeat(3) + 'if t.cursor > len(runes) {',
  T.repeat(4) + 't.cursor = len(runes)',
  T.repeat(3) + '}',
  T.repeat(3) + 'rest := append([]rune{r}, runes[t.cursor:]...)',
  T.repeat(3) + 'runes = append(runes[:t.cursor], rest...)',
  T.repeat(3) + 't.inputBuf = string(runes)',
  T.repeat(3) + 't.cursor++',
  T.repeat(3) + 't.mu.Unlock()',
  T.repeat(3) + 't.updateSuggestions()',
  T.repeat(3) + 't.render()',
  T.repeat(2) + '}',
  T + '}',
].join('\n');
s = s.replace(a2, n2);

// 3. retire the queueBuf field entirely: leftover references must go
if (s.includes('queueBuf')) { console.error('leftover queueBuf in raw_input'); process.exit(1); }
fs.writeFileSync('internal/tui/raw_input.go', s, 'utf8');
console.log('raw_input OK');

// 4. tui.go: drop queueBuf field + comment; queueLine loses 输入中 variant
let t = fs.readFileSync('internal/tui/tui.go', 'utf8');
const b = T.repeat(2) + 'queueBuf string';
must(t.includes(b), 'field');
t = t.replace(b + '\n', '');
fs.writeFileSync('internal/tui/tui.go', t, 'utf8');
console.log('field OK');
