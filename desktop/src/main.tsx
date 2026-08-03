import React from 'react';
import ReactDOM from 'react-dom/client';
import { HashRouter } from 'react-router-dom';
import App from './App';
import ErrorBoundary from './components/ErrorBoundary';
import './i18n';
import './styles/global.css';
import 'highlight.js/styles/github-dark.min.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <HashRouter>
      <ErrorBoundary>
        <App />
      </ErrorBoundary>
    </HashRouter>
  </React.StrictMode>
);

// Signal the index.html startup watchdog that the React module evaluated and
// mounting has begun. If this flag is never set, the watchdog surfaces the
// captured boot error instead of leaving a spinner spinning forever.
window.__icodeMounted = true;
window.__icodePhase = 'mounted';

// Main-thread liveness watchdog (runs on a SEPARATE Web Worker thread). If the
// UI thread ever gets stuck in a long/infinite synchronous task, the worker
// keeps ticking and records the stall to IndexedDB. On the NEXT launch the
// startup script reads that record and warns in the title bar / boot screen —
// turning an otherwise silent, unreproducible freeze into an actionable signal.
// Guarded so a Worker-less environment simply skips the diagnostic.
try {
  const workerSrc = [
    "var lastPong = Date.now();",
    "var lastPhase = '';",
    "self.onmessage = function (e) {",
    "  if (e.data && e.data.type === 'pong') { lastPong = Date.now(); lastPhase = e.data.phase || ''; }",
    "};",
    "setInterval(function () {",
    "  if (Date.now() - lastPong > 4000) {",
    "    try {",
    "      var req = indexedDB.open('icode-diag', 1);",
    "      req.onupgradeneeded = function () { req.result.createObjectStore('diag'); };",
    "      req.onsuccess = function () {",
    "        var db = req.result;",
    "        var tx = db.transaction('diag', 'readwrite');",
    "        tx.objectStore('diag').put({ type: 'ui-blocked', at: Date.now(), lastPhase: lastPhase }, 'last');",
    "        tx.oncomplete = function () { db.close(); };",
    "      };",
    "    } catch (err) {}",
    "  }",
    "  (postMessage || self.postMessage)({ type: 'ping' });",
    "}, 1000);",
  ].join('\n');
  const blob = new Blob([workerSrc], { type: 'application/javascript' });
  const worker = new Worker(URL.createObjectURL(blob));
  worker.onmessage = (e: MessageEvent) => {
    if (e.data && e.data.type === 'ping') {
      worker.postMessage({ type: 'pong', phase: window.__icodePhase || 'unknown' });
    }
  };
} catch (e) {
  // Worker unavailable (very old WebView2) — diagnostics simply absent.
}
