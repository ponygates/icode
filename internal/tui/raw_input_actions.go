package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/ponygates/icode/internal/executil"
)

// toggleTranscript implements Ctrl+O (Claude Code's transcript viewer): expand
// every tool block to show full arguments and output, or fold them all back to
// their one-line summaries. Cheap, reversible, and keeps long sessions
// readable during review.
func (t *TUI) toggleTranscript() bool {
	t.mu.Lock()
	t.transcriptVerbose = !t.transcriptVerbose
	verbose := t.transcriptVerbose
	toolCount := 0
	for i := range t.messages {
		if t.messages[i].Tool != "" {
			t.messages[i].Folded = !verbose
			toolCount++
		}
	}
	t.mu.Unlock()
	if verbose {
		t.notice(fmt.Sprintf("🔍 已展开全部工具详情（%d 条）· Ctrl+O 折叠", toolCount))
	} else {
		t.notice(fmt.Sprintf("已折叠工具详情（%d 条）· Ctrl+O 展开", toolCount))
	}
	t.render()
	return true
}

// stashPrompt implements Ctrl+S (Claude Code parity): with text in the box it
// stashes the prompt and clears the input; with an empty box it restores the
// stashed text back into the input.
func (t *TUI) stashPrompt() bool {
	t.mu.Lock()
	cur := t.inputBuf
	t.mu.Unlock()
	if strings.TrimSpace(cur) != "" {
		t.stashBuf = cur
		t.mu.Lock()
		t.inputBuf = ""
		t.cursor = 0
		t.mu.Unlock()
		t.notice("已暂存提示词（空输入时按 Ctrl+S 恢复）")
		t.render()
		return true
	}
	if t.stashBuf != "" {
		t.mu.Lock()
		t.inputBuf = t.stashBuf
		t.cursor = len([]rune(t.stashBuf))
		t.stashBuf = ""
		t.mu.Unlock()
		t.notice("已恢复暂存的提示词")
		t.render()
		return true
	}
	t.notice("没有暂存的提示词")
	return true
}

// editInExternalEditor implements Ctrl+G (Claude Code parity): hand the
// current prompt to $VISUAL/$EDITOR, then read the result back into the input
// box. Raw mode and the alternate screen are suspended for the duration so the
// editor renders normally.
func (t *TUI) editInExternalEditor() bool {
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		t.notice("未设置 $EDITOR / $VISUAL，无法用外部编辑器编辑提示词")
		return true
	}

	f, err := os.CreateTemp("", "icode-prompt-*.md")
	if err != nil {
		t.notice("创建临时文件失败: " + err.Error())
		return true
	}
	path := f.Name()
	t.mu.Lock()
	_, _ = f.WriteString(t.inputBuf)
	t.mu.Unlock()
	f.Close()
	defer os.Remove(path)

	// Split the editor value so `code -w` / `vim -u NONE` style values work.
	parts := strings.Fields(editor)
	cmd := exec.Command(parts[0], append(parts[1:], path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	t.suspendRaw()
	runErr := cmd.Run()
	t.resumeRaw()

	if runErr != nil {
		t.notice("编辑器退出异常: " + runErr.Error())
		return true
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.notice("读取编辑结果失败: " + err.Error())
		return true
	}
	edited := strings.TrimRight(string(data), "\n")
	t.mu.Lock()
	t.inputBuf = edited
	t.cursor = len([]rune(edited))
	t.mu.Unlock()
	t.notice("已载入编辑器内容（Enter 发送）")
	t.render()
	return true
}

// backgroundCurrentTurn implements Ctrl+B outside of streaming (nothing is
// running in that case) so the shortcut always gives feedback instead of
// silently doing nothing.
func (t *TUI) backgroundCurrentTurn() bool {
	if !t.streaming {
		t.notice("当前没有正在运行的任务（任务运行中按 Ctrl+B 可转入后台）")
		return true
	}
	// Streaming case is handled inside drainStream (it must break out of the
	// wait loop); reaching here means the turn just ended.
	t.notice("当前任务已接近结束，无需后台化")
	return true
}

// runShellMode implements the "! cmd" shell mode (main-loop goroutine, same
// pattern as drainStream): runs the command asynchronously, waits with
// Ctrl+C abort, prints the captured output, then hands "command + output" to
// the agent as a user turn so it can react (Claude Code parity).
func (t *TUI) runShellMode(cmdStr string) {
	t.add(RoleSystem, "$ "+cmdStr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan string, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- fmt.Sprintf("（命令执行异常: %v）", r)
			}
		}()
		done <- t.execShellCapture(ctx, cmdStr)
	}()

	var composed string
	for {
		select {
		case composed = <-done:
			// Output captured — show it and continue below.
		case r, ok := <-t.keyCh:
			if !ok {
				composed = <-done
			} else {
				traceKeySite("shell", r)
				if r == 0x03 { // Ctrl+C — kill the process, keep the transcript note
					cancel()
					t.add(RoleSystem, "⏹ shell 命令已中断")
					return
				}
				continue // other keys ignored while the command runs
			}
		}
		break
	}

	out := strings.TrimRight(composed, "\n")
	if out == "" {
		out = "（无输出）"
	}
	if len(out) > 4000 {
		out = out[:4000] + "\n…（输出已截断，完整输出可让 agent 用 bash 查看）"
	}
	t.add(RoleSystem, out)

	// Command + output enter the conversation as a user turn; the agent sees
	// the result and responds — no extra round-trip needed.
	composedMsg := fmt.Sprintf("[! shell] $ %s\n命令输出：\n%s\n\n请根据以上命令输出给出你的响应或下一步。", cmdStr, out)
	t.mu.Lock()
	t.messages = append(t.messages, Message{Role: RoleUser, Content: composedMsg})
	t.scrollOffset = 0
	t.streaming = true
	t.streamBuf.Reset()
	t.turnStart = time.Now()
	t.mu.Unlock()
	if t.callback != nil {
		go func() {
			defer func() {
				if r := recover(); r != nil {
					t.add(RoleError, fmt.Sprintf("内部错误: %v", r))
				}
			}()
			t.callback.OnSend(composedMsg, nil)
		}()
		t.ensureAnim()
		t.drainStream()
	}
}

// execShellCapture runs cmdStr through the platform shell and returns its
// combined output. It honours ctx cancellation (Ctrl+C kills the process).
func (t *TUI) execShellCapture(ctx context.Context, cmdStr string) string {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = executil.CommandContext(ctx, "cmd", "/C", cmdStr)
	} else {
		cmd = executil.CommandContext(ctx, "sh", "-c", cmdStr)
	}
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() == nil {
		// Include the exit-status hint; the output itself stays first.
		return string(out) + fmt.Sprintf("\n（退出码: %v）", err)
	}
	return string(out)
}

func (t *TUI) drainStream() {
	// Non-raw (line / piped) mode: there is no key pump and no raw-mode key
	// handling, so just wait for the stream to finish. The old code did a
	// `t.reader.(*bufio.Reader)` assertion here, which panicked on the
	// *os.File reader used by runLine — this branch never touches the reader.
	if !t.rawMode {
		<-t.streamDone
		t.streaming = false
		return
	}
	// Raw mode: while waiting for the stream to finish, keep reading keys so
	// Esc can interrupt. All keys come from the key pump's keyCh — the pump is
	// the ONLY reader of the terminal, and permission-prompt keys are routed
	// to permKeyCh while a prompt is pending, so this loop can never steal the
	// decision keys from PromptPermission.
drainLoop:
	for {
		select {
		case <-t.streamDone:
			// Stream finished; any keys still in keyCh are picked up on the
			// next main-loop iteration. Break out so the queued-message
			// auto-send below can run.
			t.streaming = false
			break drainLoop
		case r, ok := <-t.keyCh:
			if !ok {
				// Key pump exited (stdin EOF) — wait for the stream to end.
				<-t.streamDone
				t.streaming = false
				break drainLoop
			}
			traceKeySite("drain", r)
			// Process the key while still waiting for the stream.
			// Only a lone Esc and Ctrl+C work during streaming. Escape
			// SEQUENCES (arrow keys = ESC [ A, Home = ESC [ H, ...) start
			// with the same 0x1b byte, so wait escFollowTimeout for a
			// follow-up before treating it as a lone Esc — otherwise arrow
			// presses would interrupt the stream (the "方向键打断生成" bug).
			if r == 0x1b {
				select {
				case u, ok := <-t.keyCh:
					if !ok {
						<-t.streamDone
						t.streaming = false
						return
					}
					traceKeySite("drain", u)
					// Escape sequence follow-up (arrow/Home/End/mouse...) — not
					// an interrupt. The WHOLE sequence must be consumed: it
					// arrives as one atomic burst (key-pump reassembly), and
					// leaving its printable tail on keyCh used to feed e.g. a
					// wheel report's coordinates ("64;103;15") into the queue
					// buffer as if typed ("wheel during streaming prints
					// garbage" bug). Arrow-up recalls the oldest queued message
					// back into the typing buffer for editing (Claude Code
					// parity); every other sequence is swallowed whole.
					if u == '[' {
						select {
						case c2, ok2 := <-t.keyCh:
							if !ok2 {
								<-t.streamDone
								t.streaming = false
								return
							}
							traceKeySite("drain", c2)
							final := t.swallowCSI(t.keyCh, c2)
							if c2 == 'A' { // ↑ — recall the oldest queued draft
								t.mu.Lock()
								hasQueue := len(t.queue) > 0
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
							}
							_ = final
						case <-time.After(escSwallowWait):
							// Truncated burst — nothing more to consume.
						}
						_ = u
						continue
					}
					if u == 'O' {
						// SS3 (application-mode arrows/F-keys): one more byte,
						// swallowed — otherwise its final letter leaked into
						// the queue buffer as if typed.
						readRuneTimeout(t.keyCh, escSwallowWait)
						continue
					}
					// Alt+key or other ESC-follow: swallow the follow-up so it
					// can't reach the queue buffer either.
					_ = u
					continue
				case <-time.After(escSwallowWait):
					// Lone Esc — interrupt. escSwallowWait (not the snappier
					// escFollowTimeout) so a ConPTY-split "ESC | gap | rest"
					// burst still reassembles here instead of interrupting
					// the stream (the phantom-Esc-interrupts symptom).
					if t.callback != nil {
						t.callback.OnInterrupt()
					}
					// Immediate visual feedback: don't wait for the engine's
					// stream goroutine to unwind — the user must see the key
					// register the instant it's pressed.
					t.add(RoleSystem, "⏹ 正在中断…")
				}
			} else if r == 0x03 { // Ctrl+C
				if t.callback != nil {
					t.callback.OnInterrupt()
				}
				t.add(RoleSystem, "⏹ 正在中断…")
			} else if r == 0x02 { // Ctrl+B — background the running turn
				// The engine keeps working; only the UI stops blocking, so the
				// user can keep typing (messages queue up) while it runs.
				t.mu.Lock()
				t.backgrounded = true
				t.mu.Unlock()
				t.add(RoleSystem, "⏭ 已转入后台运行：可继续输入（消息将排队），完成后提示")
				break drainLoop
			} else {
				// Claude Code-style message queueing: everything the user
				// types while the agent works goes into the queue buffer
				// instead of being dropped. Enter enqueues, ↑ recalls the
				// oldest queued entry back into the buffer for editing.
				t.handleQueueKey(r)
			}
		}
	}
	// Turn finished — auto-send the oldest queued message as the next turn
	// (Claude Code parity: queue drains across turn boundaries). When the turn
	// was backgrounded with Ctrl+B the engine is still running, so the queue
	// stays put and EndStream flushes it on real completion.
	t.mu.Lock()
	bged := t.backgrounded
	t.mu.Unlock()
	if bged {
		return
	}
	if next := t.popQueue(); next != "" {
		t.notice("发送排队消息")
		t.submit(next)
	}
}

// handleQueueKey maintains the streaming-time input buffer + queue.
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
}

// popQueue removes and returns the oldest queued message, if any.
func (t *TUI) popQueue() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.queue) == 0 {
		return ""
	}
	next := t.queue[0]
	t.queue = t.queue[1:]
	return next
}
