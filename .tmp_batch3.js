const fs = require('fs');
const must = (c, m) => { if (!c) { console.error('FAIL ' + m); process.exit(1); } };
let s = fs.readFileSync('cmd/simpleui_windows.go', 'utf8');

// ── bridge fields ────────────────────────────────────────────────────────
let a = `\t// activeStreams tracks sessions with an in-flight engine generation`;
must(s.includes(a), 'fields');
s = s.replace(a, `\t// sendQueue holds user input typed while a turn was streaming —
\t// Claude Code message-queue parity. Drained automatically when the turn ends.
\tsendQueue []string
\t// lastThinkPush throttles live-thinking updates (≤1 per 300ms).
\tlastThinkPush time.Time
\t// activeStreams tracks sessions with an in-flight engine generation`);

// ── queue helpers + RunCommand busy check ────────────────────────────────
a = `func (b *simpleUIBridge) RunCommand(text string) {
\ttext = strings.TrimSpace(text)
\tif text == "" {
\t\treturn
\t}`;
must(s.includes(a), 'runcmd');
s = s.replace(a, `// queueLen returns the pending message-queue depth.
func (b *simpleUIBridge) queueLen() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.sendQueue)
}

// popSendQueue drains one queued message ("" when empty) and clears the
// queue hint in the UI.
func (b *simpleUIBridge) popSendQueue() string {
	b.mu.Lock()
	if len(b.sendQueue) == 0 {
		b.mu.Unlock()
		return ""
	}
	next := b.sendQueue[0]
	b.sendQueue = b.sendQueue[1:]
	b.mu.Unlock()
	b.push("uiQueue(0)")
	return next
}

func (b *simpleUIBridge) RunCommand(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
\t}
\t// Message queue (Claude Code parity): input typed while any turn is
\t// streaming is QUEUED and auto-sent when the turn ends, not dropped.
\tb.mu.Lock()
\tbusy := len(b.activeStreams) > 0
\tb.mu.Unlock()
\tif busy {
\t\tb.mu.Lock()
\t\tb.sendQueue = append(b.sendQueue, text)
\t\tn := len(b.sendQueue)
\t\tb.mu.Unlock()
\t\tb.push(fmt.Sprintf("uiQueue(%d)", n))
\t\treturn
\t}`);

// ── streaming thinking + queue drain at turn end ─────────────────────────
a = `\t\t\tcase types.EventThinking:
\t\t\t\tb.mu.Lock()
\t\t\t\tb.thinkingBuf += event.Content
\t\t\t\tif len(b.thinkingBuf) > 2000 {
\t\t\t\t\tb.thinkingBuf = b.thinkingBuf[:2000]
\t\t\t\t}
\t\t\t\tb.mu.Unlock()`;
must(s.includes(a), 'think case');
s = s.replace(a, `\t\t\tcase types.EventThinking:
\t\t\t\tb.mu.Lock()
\t\t\t\tb.thinkingBuf += event.Content
\t\t\t\tif len(b.thinkingBuf) > 2000 {
\t\t\t\t\tb.thinkingBuf = b.thinkingBuf[:2000]
\t\t\t\t}
\t\t\t\t// Live thinking line (Claude Code parity): dim italic preview
\t\t\t\t// above the transcript, throttled to one push per 300ms.
\t\t\t\tif time.Since(b.lastThinkPush) > 300*time.Millisecond {
\t\t\t\t\tb.lastThinkPush = time.Now()
\t\t\t\t\tpreview := b.thinkingBuf
\t\t\t\t\tif runes := []rune(preview); len(runes) > 300 {
\t\t\t\t\t\tpreview = string(runes[len(runes)-300:])
\t\t\t\t\t}
\t\t\t\t\tb.push(fmt.Sprintf("uiThinkingLive(%s)", jsStr(preview)))
\t\t\t\t}
\t\t\t\tb.mu.Unlock()`);

// clear live line when text arrives (the fold point)
a = `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tif lt != "" {`;
must(s.includes(a), 'text fold');
s = s.replace(a, `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tb.push("uiThinkingLive('')")
\t\t\t\tif lt != "" {`);

// done/error paths: clear live line
a = `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tb.push("uiDone()")
\t\t\t\tb.push("uiBusy(false)")
\t\t\t\tb.pushStats()
\t\t\t\treturn
\t\t\tcase types.EventError:`;
must(s.includes(a), 'done clear');
s = s.replace(a, `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tb.push("uiThinkingLive('')")
\t\t\t\tb.push("uiDone()")
\t\t\t\tb.push("uiBusy(false)")
\t\t\t\tb.pushStats()
\t\t\t\treturn
\t\t\tcase types.EventError:`);
a = `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tb.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(event.Content)))`;
must(s.includes(a), 'error clear');
s = s.replace(a, `\t\t\t\tif th != "" {
\t\t\t\t\tb.push(fmt.Sprintf("uiThinking(%s)", jsStr(th)))
\t\t\t\t}
\t\t\t\tb.push("uiThinkingLive('')")
\t\t\t\tb.push(fmt.Sprintf("uiAppend('error', %s)", jsStr(event.Content)))`);

// queue drain in the deferred cleanup (after activeStreams delete)
a = `\t\t\tb.mu.Lock()
\t\t\tdelete(b.activeStreams, sid)
\t\t\tb.mu.Unlock()
\t\t}()`;
must(s.includes(a), 'defer drain');
s = s.replace(a, `\t\t\tb.mu.Lock()
\t\t\tdelete(b.activeStreams, sid)
\t\t\tb.mu.Unlock()
\t\t\t// Auto-send queued input once this turn is fully over.
\t\t\tif next := b.popSendQueue(); next != "" {
\t\t\t\tgo b.RunCommand(next)
\t\t\t}
\t\t}()`);

// ── JS + CSS ─────────────────────────────────────────────────────────────
a = `  function uiTool(name, args) {`;
must(s.includes(a), 'js anchor');
s = s.replace(a, `  // Live thinking preview — dim italic line, replaced every 300ms while
  // the model reasons; removed on the first text token or turn end.
  function uiThinkingLive(text) {
    var el = document.getElementById('thinkLive');
    if (!text) { if (el) el.remove(); return; }
    if (!el) {
      el = document.createElement('div'); el.id = 'thinkLive';
      el.className = 'thinking-live';
      log.appendChild(el);
    }
    el.textContent = '🧠 ' + text;
    stick();
  }
  // Queued-message hint (typed while generating → auto-sent on turn end).
  function uiQueue(n) {
    var el = document.getElementById('queueHint');
    if (!el) {
      el = document.createElement('div'); el.id = 'queueHint';
      el.style.cssText = 'font-size:11px;color:#e0a745;padding:0 18px 4px;';
      var bar = document.getElementById('inputbar');
      bar.parentNode.insertBefore(el, bar);
    }
    el.textContent = n > 0 ? ('📨 已排队 ' + n + ' 条 — 当前回合结束后自动发送') : '';
  }
  function uiTool(name, args) {`);
a = `  .md-chk {`;
must(s.includes(a), 'css anchor');
s = s.replace(a, `  .thinking-live { color: #8a93a8; font-style: italic; font-size: 12px; padding: 3px 18px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .md-chk {`);
fs.writeFileSync('cmd/simpleui_windows.go', s, 'utf8');
console.log('simpleui OK');
