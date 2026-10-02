import * as vscode from 'vscode';
import * as cp from 'child_process';
import * as fs from 'fs';
import * as os from 'os';
import * as http from 'http';
import * as path from 'path';

// ── iCode backend discovery / lifecycle ──────────────────────────────────────

let backendPort: number | null = null;
// Per-launch Bearer token for the privileged mutating endpoints
// (shell/config/permission/update). Populated from the port file written by
// backends ≥ v0.1.3; empty for legacy plain-number port files (those
// backends don't require a token, so sending none is compatible both ways).
let backendToken: string = '';
let serverProc: cp.ChildProcess | null = null;
let output: vscode.OutputChannel | null = null;
let statusBar: vscode.StatusBarItem | null = null;

function log(msg: string): void {
  if (output) { output.appendLine(msg); }
}

function cfg<T>(key: string, def: T): T {
  return vscode.workspace.getConfiguration('icode').get<T>(key, def);
}

function updateStatusBar(port: number | null): void {
  if (!statusBar) { return; }
  if (port) {
    statusBar.text = `$(zap) iCode :${port}`;
    statusBar.tooltip = `iCode 后端已连接 (http://127.0.0.1:${port})，点击打开聊天侧栏`;
    statusBar.backgroundColor = undefined;
  } else {
    statusBar.text = '$(circle-slash) iCode';
    statusBar.tooltip = 'iCode 后端未连接，点击打开聊天侧栏（将尝试启动）';
    statusBar.backgroundColor = new vscode.ThemeColor('statusBarItem.warningBackground');
  }
  statusBar.show();
}

function portFilePath(): string {
  const tmp = process.env.TEMP || process.env.TMPDIR || os.tmpdir();
  return path.join(tmp, 'icode', 'port');
}

// The port file is a JSON document {"port":N,"token":"..."} (backends ≥
// v0.1.3). Older backends wrote a bare port number — parse both so the
// extension stays compatible with whichever binary is installed.
function readPortInfo(): { port: number; token: string } | null {
  try {
    const p = portFilePath();
    if (fs.existsSync(p)) {
      const raw = fs.readFileSync(p, 'utf8').trim();
      try {
        const obj = JSON.parse(raw);
        const n = parseInt(obj?.port, 10);
        if (!isNaN(n)) {
          return { port: n, token: typeof obj?.token === 'string' ? obj.token : '' };
        }
      } catch {
        // Not JSON — legacy plain-number file.
        const n = parseInt(raw, 10);
        if (!isNaN(n)) { return { port: n, token: '' }; }
      }
    }
  } catch { /* ignore */ }
  return null;
}

// Legacy alias: just the port number from the port file.
function readPortFile(): number | null {
  return readPortInfo()?.port ?? null;
}

// Sync the Bearer token whenever a backend port is (re)adopted: adopt the
// port file's token when it belongs to that port, otherwise clear it (that
// backend either predates tokens or belongs to another launch).
function refreshTokenFor(port: number): void {
  const info = readPortInfo();
  backendToken = info && info.port === port && info.token ? info.token : '';
}

function httpGetJSON(port: number, p: string, timeoutMs = 1500): Promise<any> {
  return new Promise((resolve, reject) => {
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (backendToken) { headers.Authorization = `Bearer ${backendToken}`; }
    const req = http.get(
      { host: '127.0.0.1', port, path: p, headers },
      (res) => {
        let data = '';
        res.on('data', (c: Buffer) => { data += c.toString(); });
        res.on('end', () => {
          try { resolve(JSON.parse(data)); } catch { resolve(data); }
        });
      }
    );
    req.on('error', reject);
    req.setTimeout(timeoutMs, () => { req.destroy(new Error('timeout')); });
  });
}

async function ensureBackend(): Promise<number> {
  // Cached port — verify it is still alive.
  if (backendPort !== null) {
    try {
      await httpGetJSON(backendPort, '/api/health');
      refreshTokenFor(backendPort);
      updateStatusBar(backendPort);
      return backendPort;
    } catch {
      backendPort = null;
      updateStatusBar(null);
    }
  }

  // 0. User-configured fixed port takes priority (pairs with backend v0.20 server.port).
  const fixed = cfg<number>('serverPort', 0);
  if (fixed > 0) {
    try {
      await httpGetJSON(fixed, '/api/health');
      backendPort = fixed;
      refreshTokenFor(fixed);
      updateStatusBar(fixed);
      return fixed;
    } catch { /* fall through to discovery/start */ }
  }

  // 1. Port file written by the iCode backend.
  let port = readPortFile();
  if (port) {
    try {
      await httpGetJSON(port, '/api/health');
      backendPort = port;
      refreshTokenFor(port);
      updateStatusBar(port);
      return port;
    } catch { /* not alive */ }
  }

  // 2. Probe well-known ports (mirrors desktop's localhost discovery).
  for (const p of [57356, 8080, 3000]) {
    try {
      await httpGetJSON(p, '/api/health');
      backendPort = p;
      refreshTokenFor(p);
      updateStatusBar(p);
      return p;
    } catch { /* next */ }
  }

  // 3. Not running — try to start it ourselves (unless disabled).
  if (!cfg<boolean>('autoStartBackend', true)) {
    throw new Error(
      'iCode 后端未运行，且已禁用自动启动（icode.autoStartBackend=false）。请先运行 `icode server`。'
    );
  }
  startBackend();
  for (let i = 0; i < 60; i++) {
    const pp = readPortFile() || (fixed > 0 ? fixed : null);
    if (pp) {
      try {
        await httpGetJSON(pp, '/api/health');
        backendPort = pp;
        refreshTokenFor(pp);
        updateStatusBar(pp);
        return pp;
      } catch { /* not ready yet */ }
    }
    await new Promise((r) => setTimeout(r, 500));
  }

  updateStatusBar(null);
  throw new Error(
    'iCode 后端未能启动。请确认已安装 iCode (https://github.com/ponygates/icode)，或先在终端运行 `icode server`。'
  );
}

function findIcodeBin(): string | null {
  // User-configured path takes priority.
  const configured = cfg<string>('binPath', '');
  if (configured) {
    if (fs.existsSync(configured)) { return configured; }
    vscode.window.showWarningMessage(`icode.binPath 指向的文件不存在: ${configured}，回退到自动查找。`);
  }
  const ext = process.platform === 'win32' ? '.exe' : '';
  const envPath = process.env.PATH || '';
  for (const dir of envPath.split(path.delimiter)) {
    if (!dir) { continue; }
    const candidate = path.join(dir, 'icode' + ext);
    if (fs.existsSync(candidate)) { return candidate; }
  }
  const home = os.homedir();
  const candidates = [
    path.join(home, 'go', 'bin', 'icode' + ext),
    path.join(home, '.local', 'bin', 'icode' + ext),
    path.join(home, '.cargo', 'bin', 'icode' + ext),
    '/usr/local/bin/icode',
    '/opt/homebrew/bin/icode',
    '/usr/bin/icode',
  ];
  for (const c of candidates) {
    if (fs.existsSync(c)) { return c; }
  }
  return null;
}

function startBackend(): void {
  if (serverProc) { return; }
  const bin = findIcodeBin();
  if (!bin) {
    vscode.window.showErrorMessage(
      '未找到 icode 可执行文件。请先安装 iCode: https://github.com/ponygates/icode'
    );
    return;
  }
  output = vscode.window.createOutputChannel('iCode');
  log('启动 iCode 后端: ' + bin);
  try {
    serverProc = cp.spawn(bin, ['server'], { env: process.env, stdio: ['ignore', 'pipe', 'pipe'] });
    serverProc.stdout?.on('data', (d: Buffer) => log(d.toString()));
    serverProc.stderr?.on('data', (d: Buffer) => log(d.toString()));
    serverProc.on('exit', (code) => { log('后端进程退出，code=' + code); serverProc = null; });
    serverProc.on('error', (e: Error) => {
      log('启动失败: ' + e.message);
      vscode.window.showErrorMessage('启动 iCode 后端失败: ' + e.message);
    });
  } catch (e: any) {
    vscode.window.showErrorMessage('启动 iCode 后端异常: ' + e.message);
  }
}

// ── REST / SSE proxy (extension process → localhost backend) ─────────────────

function proxyJSON(port: number, method: string, p: string, body?: any): Promise<any> {
  return new Promise((resolve, reject) => {
    const data = body ? JSON.stringify(body) : '';
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Content-Length': String(Buffer.byteLength(data)),
    };
    if (backendToken) { headers.Authorization = `Bearer ${backendToken}`; }
    const req = http.request(
      {
        host: '127.0.0.1',
        port,
        path: p,
        method,
        headers,
      },
      (res) => {
        let d = '';
        res.on('data', (c: Buffer) => { d += c.toString(); });
        res.on('end', () => {
          try { resolve(JSON.parse(d)); } catch { resolve(d); }
        });
      }
    );
    req.on('error', reject);
    if (data) { req.write(data); }
    req.end();
  });
}

function proxyChat(
  port: number,
  body: any,
  webview: vscode.Webview,
  sessionId: string
): void {
  const data = JSON.stringify(body);
  const chatHeaders: Record<string, string> = {
    'Content-Type': 'application/json',
    'Content-Length': String(Buffer.byteLength(data)),
  };
  if (backendToken) { chatHeaders.Authorization = `Bearer ${backendToken}`; }
  const req = http.request(
    {
      host: '127.0.0.1',
      port,
      path: '/api/chat',
      method: 'POST',
      headers: chatHeaders,
    },
    (res) => {
      let buf = '';
      res.on('data', (chunk: Buffer) => {
        buf += chunk.toString();
        let idx: number;
        while ((idx = buf.indexOf('\n\n')) >= 0) {
          const raw = buf.slice(0, idx);
          buf = buf.slice(idx + 2);
          const line = raw.trim();
          if (line.startsWith('data:')) {
            const json = line.slice(5).trim();
            if (!json) { continue; }
            try {
              const ev = JSON.parse(json);
              webview.postMessage({ type: 'stream', sessionId, event: ev });
            } catch { /* ignore malformed event */ }
          }
        }
      });
      res.on('end', () => webview.postMessage({ type: 'streamEnd', sessionId }));
      res.on('error', (e: any) => webview.postMessage({ type: 'error', sessionId, message: String(e) }));
    }
  );
  req.on('error', (e: any) => webview.postMessage({ type: 'error', sessionId, message: String(e) }));
  req.write(data);
  req.end();
}

// ── inline edit helpers (extension host side) ────────────────────────────────

// Resolve the cwd for a session: the workspace folder of the active document,
// falling back to the first workspace folder (multi-root aware).
function sessionCwd(): string | undefined {
  const ed = vscode.window.activeTextEditor;
  const folder = ed
    ? vscode.workspace.getWorkspaceFolder(ed.document.uri)
    : vscode.workspace.workspaceFolders?.[0];
  return folder?.uri.fsPath;
}

// First model the backend advertises — used as the default for headless calls
// (inline edit) that have no model picker.
async function pickDefaultModel(port: number): Promise<{ model: string; provider: string }> {
  try {
    const list = await proxyJSON(port, 'GET', '/api/models');
    const m = Array.isArray(list) ? list[0] : null;
    if (m) { return { model: m.model_id || m.id || '', provider: m.provider || '' }; }
  } catch { /* ignore */ }
  return { model: '', provider: '' };
}

// One-shot chat: POST /api/chat, read the SSE stream, and resolve with the
// concatenated assistant text once the stream ends. Reuses the same backend
// endpoint the ask/explain/improve commands drive through the webview.
function chatComplete(port: number, body: any, timeoutMs = 60000): Promise<string> {
  return new Promise((resolve, reject) => {
    const data = JSON.stringify(body);
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      'Content-Length': String(Buffer.byteLength(data)),
    };
    if (backendToken) { headers.Authorization = `Bearer ${backendToken}`; }
    const req = http.request(
      {
        host: '127.0.0.1', port, path: '/api/chat', method: 'POST',
        headers,
      },
      (res) => {
        let buf = '';
        let out = '';
        res.on('data', (chunk: Buffer) => {
          buf += chunk.toString();
          let idx: number;
          while ((idx = buf.indexOf('\n\n')) >= 0) {
            const raw = buf.slice(0, idx);
            buf = buf.slice(idx + 2);
            const line = raw.trim();
            if (!line.startsWith('data:')) { continue; }
            try {
              const ev = JSON.parse(line.slice(5).trim());
              if (ev.type === 'text') { out += ev.content || ''; }
            } catch { /* ignore malformed event */ }
          }
        });
        res.on('end', () => resolve(out));
        res.on('error', reject);
      }
    );
    req.setTimeout(timeoutMs, () => req.destroy(new Error('timeout')));
    req.on('error', reject);
    req.write(data);
    req.end();
  });
}

// Pull the first fenced code block out of a model reply; null when there is
// none (we then abort the edit rather than paste prose over the selection).
function extractFencedCode(text: string): string | null {
  const m = text.match(/```[a-zA-Z0-9_+-]*\r?\n([\s\S]*?)```/);
  if (m) { return m[1].replace(/\r?\n$/, ''); }
  return null;
}

// ── Webview view provider ────────────────────────────────────────────────────

interface WebviewMessage {
  type: string;
  [key: string]: any;
}

class ICodeViewProvider implements vscode.WebviewViewProvider {
  constructor(private readonly ext: ICodeExtension) {}

  resolveWebviewView(webviewView: vscode.WebviewView): void {
    webviewView.webview.options = { enableScripts: true, enableCommandUris: false };
    webviewView.webview.html = this.ext.getHtml(webviewView.webview);
    webviewView.webview.onDidReceiveMessage(async (msg: WebviewMessage) => {
      await this.ext.handleMessage(msg, webviewView.webview);
    });
    this.ext.attachWebview(webviewView.webview);
  }
}

class ICodeExtension {
  private webview: vscode.Webview | null = null;
  private pendingPrompt: { text: string; autoSend: boolean } | null = null;

  attachWebview(webview: vscode.Webview): void {
    this.webview = webview;
    if (this.pendingPrompt) {
      const p = this.pendingPrompt;
      this.pendingPrompt = null;
      // Give the webview a moment to boot before injecting.
      setTimeout(() => {
        webview.postMessage({ type: 'insertPrompt', text: p.text, autoSend: p.autoSend });
      }, 600);
    }
  }

  /** Build a fenced prompt from the current editor selection and hand it to the sidebar. */
  private async sendSelectionToChat(instruction: string, autoSend: boolean): Promise<void> {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.selection.isEmpty) {
      vscode.window.showInformationMessage('请先在编辑器中选中一段代码。');
      return;
    }
    const doc = editor.document;
    const sel = editor.selection;
    const code = doc.getText(sel);
    const rel = vscode.workspace.asRelativePath(doc.uri, false);
    const lang = doc.languageId || '';
    const startLine = sel.start.line + 1;
    const endLine = sel.end.line + 1;
    const header = `${rel}:${startLine}-${endLine}`;
    const text = `${instruction}\n\n\`${header}\`\n\`\`\`${lang}\n${code}\n\`\`\``;

    if (this.webview) {
      this.webview.postMessage({ type: 'insertPrompt', text, autoSend });
    } else {
      this.pendingPrompt = { text, autoSend };
    }
    await vscode.commands.executeCommand('icode.chat.focus');
  }

  /** Inline edit: send the selection + an instruction to the backend, then
   *  apply the returned code over the selection as a single undoable edit. */
  private async editSelection(): Promise<void> {
    const editor = vscode.window.activeTextEditor;
    if (!editor || editor.selection.isEmpty) {
      vscode.window.showInformationMessage('请先在编辑器中选中要修改的代码。');
      return;
    }
    const instruction = await vscode.window.showInputBox({
      prompt: 'iCode 内联编辑：描述你想要的改动',
      placeHolder: '例如：改用早返回，并为空值补充错误处理',
      ignoreFocusOut: true,
    });
    if (!instruction) { return; }

    const doc = editor.document;
    const code = doc.getText(editor.selection);
    const rel = vscode.workspace.asRelativePath(doc.uri, false);
    let port: number;
    try {
      port = await ensureBackend();
    } catch (e: any) {
      vscode.window.showErrorMessage(e.message);
      return;
    }
    const { model, provider } = await pickDefaultModel(port);
    // An isolated session keeps the edit turn out of the sidebar conversation.
    const sid = 'edit-' + Date.now().toString(36);
    await proxyJSON(port, 'POST', '/api/sessions', {
      id: sid, title: 'inline edit', model, provider, cwd: sessionCwd(),
    }).catch(() => { /* backend may auto-create on chat */ });

    const content =
      `${instruction}\n\n下面是文件 ${rel} 中被选中的代码，请只返回修改后的完整代码` +
      `（用 \`\`\` 代码块包裹，保持原有缩进），不要输出解释文字：\n\`\`\`\n${code}\n\`\`\``;

    await vscode.window.withProgress(
      { location: vscode.ProgressLocation.Notification, title: 'iCode 正在编辑选中代码…' },
      async () => {
        let reply: string;
        try {
          reply = await chatComplete(port, { session_id: sid, content, model, provider });
        } catch (e: any) {
          vscode.window.showErrorMessage('iCode 编辑失败: ' + e.message);
          return;
        }
        const newCode = extractFencedCode(reply);
        if (newCode == null) {
          vscode.window.showWarningMessage('模型未返回代码块，已取消编辑。');
          return;
        }
        // The document may have been edited while the model was thinking:
        // re-validate the selection text so a stale replacement can never
        // clobber the user's own keystrokes.
        if (doc.isDirty || doc.getText(editor.selection) !== code) {
          vscode.window.showWarningMessage('文件在生成期间已发生改动，为安全起见已取消应用。');
          return;
        }
        // Cursor-style review flow: open a full-file diff (original vs
        // edited) instead of writing directly to the document, and let the
        // user apply or discard after inspecting the change.
        const selStart = doc.offsetAt(editor.selection.start);
        const selEnd = doc.offsetAt(editor.selection.end);
        const fullText = doc.getText();
        const fullNew = fullText.slice(0, selStart) + newCode + fullText.slice(selEnd);
        const modified = await vscode.workspace.openTextDocument({
          content: fullNew, language: doc.languageId,
        });
        await vscode.commands.executeCommand('vscode.diff',
          doc.uri, modified.uri, `iCode 编辑: ${rel} — 审阅后应用`);
        const closeDiff = async (): Promise<void> => {
          for (const ed of vscode.window.visibleTextEditors) {
            if (ed.document.uri.toString() === modified.uri.toString()) {
              await vscode.window.showTextDocument(modified, { viewColumn: ed.viewColumn });
              await vscode.commands.executeCommand('workbench.action.closeActiveEditor');
              break;
            }
          }
        };
        const pick = await vscode.window.showQuickPick(
          [{ label: '$(check) 应用修改', apply: true }, { label: '$(close) 放弃', apply: false }],
          { placeHolder: '请在 diff 视图中审阅 AI 的修改，再选择是否应用', ignoreFocusOut: true },
        );
        if (!pick || !pick.apply) {
          await closeDiff();
          return;
        }
        // Re-validate once more: the user may have typed in the document
        // while reviewing the diff side by side.
        if (doc.getText(editor.selection) !== code) {
          vscode.window.showWarningMessage('文件在审阅期间被修改，已取消应用。');
          await closeDiff();
          return;
        }
        // WorkspaceEdit.replace is atomic and lands on the undo stack.
        const edit = new vscode.WorkspaceEdit();
        edit.replace(doc.uri, editor.selection, newCode);
        const applied = await vscode.workspace.applyEdit(edit);
        await closeDiff();
        if (!applied) { vscode.window.showErrorMessage('应用编辑失败。'); }
      }
    );
  }

  async activate(ctx: vscode.ExtensionContext): Promise<void> {
    output = vscode.window.createOutputChannel('iCode');
    const provider = new ICodeViewProvider(this);
    ctx.subscriptions.push(
      vscode.window.registerWebviewViewProvider('icode.chat', provider, {
        webviewOptions: { retainContextWhenHidden: true },
      })
    );

    // Status bar: backend connectivity at a glance; click opens the sidebar.
    statusBar = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 90);
    statusBar.command = 'icode.openSidebar';
    updateStatusBar(null);
    ctx.subscriptions.push(statusBar);
    const healthTimer = setInterval(async () => {
      if (backendPort === null) { updateStatusBar(null); return; }
      try {
        await httpGetJSON(backendPort, '/api/health');
        updateStatusBar(backendPort);
      } catch {
        backendPort = null;
        updateStatusBar(null);
      }
    }, 15000);
    ctx.subscriptions.push({ dispose: () => clearInterval(healthTimer) });

    ctx.subscriptions.push(
      vscode.commands.registerCommand('icode.openSidebar', () => {
        vscode.commands.executeCommand('icode.chat.focus');
      }),
      vscode.commands.registerCommand('icode.startServer', async () => {
        try {
          const port = await ensureBackend();
          vscode.window.showInformationMessage(`iCode 后端已就绪: http://127.0.0.1:${port}`);
        } catch (e: any) {
          vscode.window.showErrorMessage(e.message);
        }
      }),
      vscode.commands.registerCommand('icode.openInTerminal', () => {
        const term = vscode.window.createTerminal('iCode');
        term.sendText('icode');
        term.show();
      }),
      vscode.commands.registerCommand('icode.openSettings', () => {
        vscode.commands.executeCommand('workbench.action.openSettings', 'icode');
      }),
      vscode.commands.registerCommand('icode.askSelection', () => {
        return this.sendSelectionToChat('关于下面这段代码，我想问：', false);
      }),
      vscode.commands.registerCommand('icode.explainSelection', () => {
        return this.sendSelectionToChat('请解释下面这段代码的作用、关键逻辑与潜在问题：', true);
      }),
      vscode.commands.registerCommand('icode.improveSelection', () => {
        return this.sendSelectionToChat('请优化下面这段代码（可读性/性能/健壮性），给出改进后的完整代码与说明：', true);
      }),
      vscode.commands.registerCommand('icode.editSelection', () => {
        return this.editSelection();
      })
    );
    log('iCode 扩展已激活');
  }

  getHtml(webview: vscode.Webview): string {
    const nonce = getNonce();
    const htmlPath = path.join(__dirname, '..', 'media', 'sidebar.html');
    let html = '';
    try {
      html = fs.readFileSync(htmlPath, 'utf8');
    } catch {
      html = '<html><body>iCode 资源缺失 (media/sidebar.html)</body></html>';
    }
    return html.replace(/\$\{nonce\}/g, nonce);
  }

  async handleMessage(msg: WebviewMessage, webview: vscode.Webview): Promise<void> {
    try {
      const port = await ensureBackend();
      switch (msg.type) {
        case 'ready':
          webview.postMessage({ type: 'status', port });
          break;
        case 'listSessions': {
          const sessions = await proxyJSON(port, 'GET', '/api/sessions');
          webview.postMessage({ type: 'sessions', payload: sessions || [] });
          break;
        }
        case 'listModels': {
          const models = await proxyJSON(port, 'GET', '/api/models');
          webview.postMessage({ type: 'models', payload: models || [] });
          break;
        }
        case 'createSession': {
          const sess = await proxyJSON(port, 'POST', '/api/sessions', {
            id: msg.id,
            title: msg.title,
            model_id: msg.model,
            provider_name: msg.provider,
            // Multi-root aware: bind the session to the active document's
            // workspace folder so the backend resolves relative paths there.
            cwd: sessionCwd(),
          });
          webview.postMessage({ type: 'sessionCreated', session: sess });
          break;
        }
        case 'listFiles': {
          // `@`-context picker: match workspace files against the query and
          // echo back relative paths the backend can later resolve.
          const safe = String(msg.query || '').replace(/[^\w.\-/ ]/g, '');
          const pattern = safe ? `**/*${safe}*` : '**/*';
          const uris = await vscode.workspace.findFiles(pattern, '**/node_modules/**', 60);
          const files = uris.map((u) => vscode.workspace.asRelativePath(u, false));
          webview.postMessage({ type: 'files', reqId: msg.reqId, payload: files });
          break;
        }
        case 'send':
          proxyChat(
            port,
            {
              session_id: msg.sessionId,
              content: msg.content,
              model: msg.model,
              provider: msg.provider,
              cwd: sessionCwd(),
            },
            webview,
            msg.sessionId
          );
          break;
        case 'stop':
          await proxyJSON(port, 'POST', '/api/chat/stop', { session_id: msg.sessionId });
          break;
        case 'permissionRespond':
          await proxyJSON(port, 'POST', '/api/permission/respond', {
            request_id: msg.requestId,
            decision: msg.decision,
          });
          break;
        default:
          break;
      }
    } catch (e: any) {
      webview.postMessage({ type: 'error', message: e.message });
    }
  }
}

function getNonce(): string {
  let text = '';
  const chars = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  for (let i = 0; i < 32; i++) {
    text += chars.charAt(Math.floor(Math.random() * chars.length));
  }
  return text;
}

export function activate(ctx: vscode.ExtensionContext): void {
  new ICodeExtension().activate(ctx);
}

export function deactivate(): void {
  if (serverProc) {
    try { serverProc.kill(); } catch { /* ignore */ }
  }
}
