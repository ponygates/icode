const fs = require('fs');
const must = (c, m) => { if (!c) { console.error('FAIL ' + m); process.exit(1); } };
let s = fs.readFileSync('cmd/simpleui_windows.go', 'utf8');

// payload fields
let a = `type uiStatsPayload struct {
\tModel        string  \`json:"model"\``;
must(s.includes(a), 'payload');
s = s.replace(a, `type uiStatsPayload struct {
\tGitBranch    string  \`json:"git_branch,omitempty"\`
\tElapsedSec   int     \`json:"elapsed_sec,omitempty"\`
\tModel        string  \`json:"model"\``);

// bridge fields
a = `\t// sendQueue holds user input typed while a turn was streaming —`;
must(s.includes(a), 'fields');
s = s.replace(a, `\t// gitBranch / gitBranchAt cache the branch segment (spawned at most
\t// once per 30s, not per status refresh).
\tgitBranch   string
\tgitBranchAt time.Time
\t// turnStartedAt marks the in-flight turn for the ⏱ segment.
\tturnStartedAt time.Time
\t// sendQueue holds user input typed while a turn was streaming —`);

// mark turn start where the stream registers
a = `\tb.activeStreams[sid] = true`;
must(s.includes(a), 'stream start');
s = s.replace(a, `\tb.activeStreams[sid] = true
\tb.turnStartedAt = time.Now()`);

// statsJSON: git + elapsed segments
a = `\tv, err := json.Marshal(&p)
\tif err != nil {
\t\treturn "{}"
\t}
\treturn string(v)
}`;
must(s.includes(a), 'stats tail');
s = s.replace(a, `\t// Git branch segment — cached 30s, one child process at most.
\tb.mu.Lock()
\tif b.gitBranch == "" || time.Since(b.gitBranchAt) > 30*time.Second {
\t\tb.gitBranchAt = time.Now()
\t\tif out, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
\t\t\tb.gitBranch = strings.TrimSpace(string(out))
\t\t}
\t}
\tbranch := b.gitBranch
\tb.mu.Unlock()
\tp.GitBranch = branch
\tb.mu.Lock()
\tbusy := len(b.activeStreams) > 0
\tstart := b.turnStartedAt
\tb.mu.Unlock()
\tif busy && !start.IsZero() {
\t\tp.ElapsedSec = int(time.Since(start).Seconds())
\t}
\tv, err := json.Marshal(&p)
\tif err != nil {
\t\treturn "{}"
\t}
\treturn string(v)
}`);

// JS segments
a = `    if (s.cost > 0) parts.push('¥' + s.cost.toFixed(4));
    el.textContent = parts.join('  ·  ');`;
must(s.includes(a), 'js');
s = s.replace(a, `    if (s.git_branch) parts.push('⎇ ' + s.git_branch);
    if (s.cost > 0) parts.push('¥' + s.cost.toFixed(4));
    if (s.elapsed_sec > 0) parts.push('⏱ ' + s.elapsed_sec + 's');
    el.textContent = parts.join('  ·  ');`);
fs.writeFileSync('cmd/simpleui_windows.go', s, 'utf8');
console.log('OK');
