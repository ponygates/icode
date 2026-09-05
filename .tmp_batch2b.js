const fs = require('fs');
const must = (c, m) => { if (!c) { console.error('FAIL ' + m); process.exit(1); } };

// ═══ S1: Host header validation (DNS-rebinding defence) ═══
let s = fs.readFileSync('internal/server/server.go', 'utf8');
let a = `	handler := s.corsMiddleware(s.authMiddleware(sameOriginGuard(s.serverOrigin, recoverMiddleware(mux))))`;
must(s.includes(a), 'chain');
s = s.replace(a, `	handler := s.corsMiddleware(s.authMiddleware(sameOriginGuard(s.hostGuard(recoverMiddleware(mux)))))`);
a = `// sameOriginGuard rejects state-changing cross-origin requests (CSRF). The`;
must(s.includes(a), 'so anchor');
s = s.replace(a, `// hostGuard rejects requests whose Host header is not loopback. The server
// binds 127.0.0.1 only, so any other Host value is a DNS-rebinding attempt
// (attacker page resolves a hostname to 127.0.0.1 and same-origin policies
// then treat the requests as same-site). GET reads like /api/files and
// /api/sessions would otherwise be readable cross-origin.
func (s *Server) hostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(r.Host); err == nil {
			host = h
		}
		switch host {
		case "127.0.0.1", "::1", "localhost":
			next.ServeHTTP(w, r)
		default:
			http.Error(w, "forbidden host", http.StatusForbidden)
		}
	})
}

// sameOriginGuard rejects state-changing cross-origin requests (CSRF). The`);
fs.writeFileSync('internal/server/server.go', s, 'utf8');
console.log('host OK');

// ═══ S2: sandbox EvalSymlinks + Windows case-insensitivity ═══
let g = fs.readFileSync('internal/core/permission/gate.go', 'utf8');
a = `\tabs, err := filepath.Abs(action.Path)
\tif err != nil {
\t\treturn true
\t}
\tfor _, ap := range g.AllowedPaths {
\t\tapAbs, err := filepath.Abs(ap)
\t\tif err != nil {
\t\t\tcontinue
\t\t}
\t\t// Match on a path-boundary so allowlist /home/u/proj does not also
\t\t// permit /home/u/project2.
\t\tif abs == apAbs || strings.HasPrefix(abs, apAbs+string(os.PathSeparator)) {
\t\t\treturn false
\t\t}
\t}
\treturn true`;
must(g.includes(a), 'sandbox');
g = g.replace(a, `\tabs, err := filepath.Abs(action.Path)
\tif err != nil {
\t\treturn true
\t}
\t// Resolve symlinks on both sides so a symlink inside the sandbox cannot
\t// point outside it; on failure fall back to the lexical path (missing
\t// files are common for write targets — the target DIR is what matters,
\t// and the lexical Abs path is still checked below).
\tif resolved, err := filepath.EvalSymlinks(abs); err == nil {
\t\tabs = resolved
\t}
\tfor _, ap := range g.AllowedPaths {
\t\tapAbs, err := filepath.Abs(ap)
\t\tif err != nil {
\t\t\tcontinue
\t\t}
\t\tif resolved, err := filepath.EvalSymlinks(apAbs); err == nil {
\t\t\tapAbs = resolved
\t\t}
\t\t// Match on a path-boundary so allowlist /home/u/proj does not also
\t\t// permit /home/u/project2. Windows filesystems are case-insensitive.
\t\tif pathsEquivalent(abs, apAbs) || pathsWithinDir(abs, apAbs) {
\t\t\treturn false
\t\t}
\t}
\treturn true`);
a = `func (g *Gate) isDenied(action Action) bool {`;
must(g.includes(a), 'isDenied');
g = g.replace(a, `// pathsEquivalent compares two absolute paths, case-insensitively on
// case-insensitive filesystems (Windows).
func pathsEquivalent(a, b string) bool {
	if a == b {
		return true
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return false
}

// pathsWithinDir reports whether child lives under dir (path-boundary aware,
// case-insensitive on Windows).
func pathsWithinDir(child, dir string) bool {
	rel, err := filepath.Rel(dir, child)
	if err != nil {
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return false
	}
	if runtime.GOOS == "windows" {
		return true // Rel already resolved the case-sensitive comparison
	}
	return true
}

func (g *Gate) isDenied(action Action) bool {`);
fs.writeFileSync('internal/core/permission/gate.go', g, 'utf8');
console.log('sandbox OK');

// ═══ S3: mesh constant-time token compare ═══
let m = fs.readFileSync('internal/mesh/mesh.go', 'utf8');
a = `\treturn presented == want`;
must(m.includes(a), 'mesh');
m = m.replace(a, `\t// Constant-time compare: a loopback listener still should not leak the
\t// token length/content through timing.
\treturn subtle.ConstantTimeCompare([]byte(presented), []byte(want)) == 1`);
if (!m.includes('"crypto/subtle"')) {
  m = m.replace('\t"crypto/rand"', '\t"crypto/rand"\n\t"crypto/subtle"');
}
fs.writeFileSync('internal/mesh/mesh.go', m, 'utf8');
console.log('mesh OK');

// ═══ E4: budget knob ═══
let c = fs.readFileSync('internal/config/config.go', 'utf8');
a = `\t// MaxToolRounds caps agent tool iterations per turn (0 = default 25).
\tMaxToolRounds int \`yaml:"max_tool_rounds" json:"max_tool_rounds"\``;
must(c.includes(a), 'cfg budget');
c = c.replace(a, a + `
\t// BudgetGlobalChars caps total tool-output characters per turn
\t// (tokenopt Level 4 budget enforcer; 0 = default 200000).
\tBudgetGlobalChars int \`yaml:"budget_global_chars" json:"budget_global_chars"\``);
fs.writeFileSync('internal/config/config.go', c, 'utf8');
console.log('cfg budget OK');

let ap = fs.readFileSync('internal/app/app.go', 'utf8');
a = `\tif cfg.Tools.MaxToolRounds > 0 {
\t\tapp.Engine.SetMaxToolRounds(cfg.Tools.MaxToolRounds)
\t}`;
must(ap.includes(a), 'app budget');
ap = ap.replace(a, a + `
\tif cfg.Tools.BudgetGlobalChars > 0 {
\t\tapp.Engine.SetBudgetGlobalChars(cfg.Tools.BudgetGlobalChars)
\t}`);
fs.writeFileSync('internal/app/app.go', ap, 'utf8');
console.log('app budget OK');

let e = fs.readFileSync('internal/core/conversation/engine.go', 'utf8');
a = `// SetMaxToolRounds sets the agent tool-iteration cap per turn (0 = default).`;
must(e.includes(a), 'engine budget');
e = e.replace(a, `// SetBudgetGlobalChars overrides the Level-4 budget enforcer's total
// tool-output character cap per turn (0 = tokenopt default).
func (e *Engine) SetBudgetGlobalChars(n int) {
	e.mu.Lock()
	e.budgetGlobalChars = n
	e.mu.Unlock()
}

// SetMaxToolRounds sets the agent tool-iteration cap per turn (0 = default).`);
a = `\t// humanizeLLMPolish gates the extra denial-rewrite LLM call (default off).`;
must(e.includes(a), 'budget field');
e = e.replace(a, `\t// budgetGlobalChars overrides the Level-4 budget enforcer cap (0 = default).
\tbudgetGlobalChars int
\t// humanizeLLMPolish gates the extra denial-rewrite LLM call (default off).`);
a = `\t\tbudgetEnforcer: tokenopt.NewBudgetEnforcer(tokenopt.DefaultBudgetConfig()),`;
must(e.includes(a), 'budget init');
e = e.replace(a, `\t\tbudgetEnforcer: tokenopt.NewBudgetEnforcer(budgetConfig()),`);
a = `// SetBudgetGlobalChars overrides`;
must(e.includes(a), 'bcfg helper');
e = e.replace(a, `// budgetConfig honours the user's budget_global_chars override on top of
// the tokenopt defaults.
func (e *Engine) budgetConfig() tokenopt.BudgetConfig {
	bc := tokenopt.DefaultBudgetConfig()
	e.mu.Lock()
	n := e.budgetGlobalChars
	e.mu.Unlock()
	if n > 0 {
		bc.GlobalMax = n
	}
	return bc
}

// SetBudgetGlobalChars overrides`);
fs.writeFileSync('internal/core/conversation/engine.go', e, 'utf8');
console.log('engine budget OK');
