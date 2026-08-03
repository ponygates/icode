import React, { useState, useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import hljs from 'highlight.js';

// ── styles ──
const codeBlock: React.CSSProperties = {
  background: 'var(--bg-primary)',
  border: '0.5px solid var(--border-color)',
  borderRadius: 8, padding: '12px 14px', lineHeight: 1.6,
  overflowX: 'auto', fontSize: 12.5,
  fontFamily: 'var(--font-mono)', color: 'var(--text-primary)', margin: 0,
};
const codeHead: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
  padding: '5px 12px', background: 'var(--bg-tertiary)',
  border: '0.5px solid var(--border-color)', borderBottom: 'none',
  borderRadius: '8px 8px 0 0', fontSize: 11, color: 'var(--text-secondary)',
  fontFamily: 'var(--font-mono)', letterSpacing: '0.02em',
};
const icoStyle: React.CSSProperties = {
  background: 'var(--bg-primary)', border: '0.5px solid var(--border-color)',
  borderRadius: 5, padding: '1px 6px', fontSize: '0.92em', fontFamily: 'var(--font-mono)',
};
const copyBtn: React.CSSProperties = {
  background: 'transparent', border: '0.5px solid var(--border-color)',
  borderRadius: 5, color: 'var(--text-secondary)', cursor: 'pointer', fontSize: 11, padding: '2px 8px',
};

// ── inline renderer (Supports: **bold**, *italic*, ~~strike~~, `code`, [link](url), bare URLs) ──
function renderInline(text: string, keyBase: string): React.ReactNode[] {
  const nodes: React.ReactNode[] = [];
  // Tokenizer: match **bold**, *italic*, ~~strike~~, `code`, [text](url)
  const re = /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*|_[^_]+_|~~[^~]+~~|\[[^\]]+\]\([^)]+\))/g;
  let last = 0, m: RegExpExecArray | null, i = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) {
      // Split bare URLs in plain text segments
      nodes.push(...linkify(text.slice(last, m.index), keyBase + 'l' + i));
    }
    const tok = m[0];
    if (tok.startsWith('`')) {
      nodes.push(<code key={keyBase + i} style={icoStyle}>{tok.slice(1, -1)}</code>);
    } else if (tok.startsWith('**')) {
      nodes.push(<strong key={keyBase + i}>{tok.slice(2, -2)}</strong>);
    } else if (tok.startsWith('*')) {
      nodes.push(<em key={keyBase + i}>{tok.slice(1, -1)}</em>);
    } else if (tok.startsWith('~~')) {
      nodes.push(<del key={keyBase + i}>{tok.slice(2, -2)}</del>);
    } else if (tok.startsWith('_')) {
      nodes.push(<em key={keyBase + i}>{tok.slice(1, -1)}</em>);
    } else {
      const lm = /\[([^\]]+)\]\(([^)]+)\)/.exec(tok);
      if (lm) {
        nodes.push(
          <a key={keyBase + i} href={lm[2]} target="_blank" rel="noopener noreferrer"
            style={{ color: 'var(--accent)', textDecoration: 'underline' }}>
            {lm[1]}
          </a>,
        );
      } else { nodes.push(tok); }
    }
    last = m.index + tok.length; i++;
  }
  if (last < text.length) nodes.push(...linkify(text.slice(last), keyBase + 'z'));
  return nodes;
}

// Bare URL → clickable link
function linkify(text: string, key: string): React.ReactNode[] {
  const re = /(https?:\/\/[^\s<>"{}|\\^`\[\]]+)/g;
  const parts: React.ReactNode[] = [];
  let last = 0, m: RegExpExecArray | null, j = 0;
  while ((m = re.exec(text)) !== null) {
    if (m.index > last) parts.push(text.slice(last, m.index));
    parts.push(
      <a key={key + j} href={m[0]} target="_blank" rel="noopener noreferrer"
        style={{ color: 'var(--accent)', textDecoration: 'underline' }}>
        {m[0]}
      </a>,
    );
    last = m.index + m[0].length; j++;
  }
  if (last < text.length) parts.push(text.slice(last));
  return parts;
}

// Diff line colouring: applies green/red/cyan inline styles to unified-diff lines.
function colorizeDiffLines(code: string): React.ReactNode[] {
  const lines = code.split('\n');
  const diffAdd: React.CSSProperties = { color: 'var(--success, #4caf50)' };
  const diffDel: React.CSSProperties = { color: 'var(--error, #f44336)' };
  const diffHunk: React.CSSProperties = { color: 'var(--accent, #2196f3)' };
  const diffHead: React.CSSProperties = { fontWeight: 600 };
  return lines.map((ln, i) => {
    if (ln.startsWith('+++') || ln.startsWith('---')) return <span key={i} style={diffHead}>{ln}</span>;
    if (ln.startsWith('+')) return <span key={i} style={diffAdd}>{ln}</span>;
    if (ln.startsWith('-')) return <span key={i} style={diffDel}>{ln}</span>;
    if (ln.startsWith('@@')) return <span key={i} style={diffHunk}>{ln}</span>;
    return <span key={i}>{ln}</span>;
  }).reduce<React.ReactNode[]>((acc, node, i) => {
    if (i > 0) acc.push('\n');
    acc.push(node);
    return acc;
  }, []);
}

// HTML-escape for the safe plain-text fallback used when we skip expensive
// syntax highlighting on large code blocks.
function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;');
}

// ── code block with syntax highlighting and copy ──
const CodeBlock = React.memo(({ code, lang, streaming }: { code: string; lang: string; streaming?: boolean }) => {
  const { t } = useTranslation();
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    navigator.clipboard.writeText(code).then(() => {
      setCopied(true); setTimeout(() => setCopied(false), 2000);
    }).catch(() => {});
  };

  // Bound the input for pathological sizes: a multi-MB paste shouldn't blow up
  // the DOM or the highlighter. The copy action still uses the full `code`.
  const safeCode = code.length > 300000
    ? code.slice(0, 300000) + '\n\n…(内容过长，已截断显示)'
    : code;

  const highlighted = useMemo(() => {
    // Cap highlight work. Large blocks make hljs.highlightAuto scan every
    // grammar and can freeze the WebView2 UI for seconds — that freeze is the
    // classic "typing / switching conversation hangs" bug. Never run
    // highlightAuto (the slow path); only highlight when a language is given,
    // and skip entirely past the cap (cheap escaped plain text instead).
    const MAX_HL = 50000;
    if (safeCode.length > MAX_HL) {
      return { value: escapeHtml(safeCode), language: lang || '' };
    }
    try {
      if (lang && hljs.getLanguage(lang)) {
        return hljs.highlight(safeCode, { language: lang });
      }
      // highlightAuto scans every grammar and is O(len × langs) — fine for a
      // finished message, but during streaming it runs every frame as text
      // grows, freezing the UI on medium code blocks. Skip it while streaming;
      // the final render (streaming=false) will pick the language up if the
      // model emitted a fenced block with a language tag, or fall back to
      // escaped plain text. Never run highlightAuto mid-stream.
      if (!streaming && safeCode.length < 8000) {
        return hljs.highlightAuto(safeCode);
      }
      return { value: escapeHtml(safeCode), language: '' };
    } catch {
      return { value: escapeHtml(safeCode), language: '' };
    }
  }, [safeCode, lang, streaming]);

  return (
    <div style={{ margin: '8px 0' }}>
      <div style={codeHead}>
        <span>{lang || highlighted.language || 'code'}</span>
        <button onClick={handleCopy} style={copyBtn}>{copied ? `✓ ${t('markdown.copied')}` : t('markdown.copy')}</button>
      </div>
      <pre style={{ ...codeBlock, borderRadius: lang ? '0 0 6px 6px' : 6 }}>
        {lang === 'diff' ? <code>{colorizeDiffLines(safeCode)}</code> :
          <code dangerouslySetInnerHTML={{ __html: highlighted.value }} />}
      </pre>
    </div>
  );
});

// ── main Markdown component ──
// `streaming` is true while the assistant message is still being generated.
// It disables the expensive highlightAuto path on code blocks so the UI
// doesn't freeze on every token; the final (streaming=false) render will
// pick up highlighting then.
const Markdown = React.memo(({ text, streaming }: { text: string; streaming?: boolean }) => {
  const lines = text.split('\n');
  const blocks: React.ReactNode[] = [];
  let i = 0, key = 0;

  while (i < lines.length) {
    const line = lines[i];
    const trim = line.trim();

    // Fenced code block
    if (trim.startsWith('```')) {
      const code: string[] = [];
      const lang = trim.slice(3).trim();
      i++;
      while (i < lines.length && !lines[i].trim().startsWith('```')) {
        code.push(lines[i]); i++;
      }
      i++; // skip closing ```
      blocks.push(<CodeBlock key={key++} code={code.join('\n')} lang={lang} streaming={streaming} />);
      continue;
    }

    // Table
    if (/^\|.+\|$/.test(trim)) {
      const rows: string[][] = [];
      while (i < lines.length && /^\|.+\|$/.test(lines[i].trim())) {
        rows.push(lines[i].split('|').slice(1, -1).map(c => c.trim()));
        i++;
      }
      // skip separator row (e.g. |---|---|)
      const header = rows[0];
      const sepIdx = rows.findIndex(r => r.every(c => /^:?-{3,}:?$/.test(c)));
      const body = sepIdx >= 0 ? rows.slice(sepIdx + 1) : rows.slice(1);
      if (header && body.length >= 0) {
        blocks.push(
          <div key={key++} style={{ overflowX: 'auto', margin: '8px 0' }}>
            <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 12 }}>
              <thead>
                <tr style={{ borderBottom: '2px solid var(--border-color)' }}>
                  {header.map((c, ci) => (
                    <th key={ci} style={{ padding: '6px 10px', textAlign: 'left', color: 'var(--text-primary)', fontWeight: 600 }}>
                      {renderInline(c, 'th' + key + ci)}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {body.map((row, ri) => (
                  <tr key={ri} style={{ borderBottom: '1px solid var(--border-color)' }}>
                    {row.map((c, ci) => (
                      <td key={ci} style={{ padding: '4px 10px', color: 'var(--text-secondary)' }}>
                        {renderInline(c, 'td' + key + ri + ci)}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>,
        );
      }
      continue;
    }

    // Horizontal rule
    if (/^(-{3,}|\*{3,}|_{3,})$/.test(trim)) {
      blocks.push(<hr key={key++} style={{ border: 'none', borderTop: '1px solid var(--border-color)', margin: '12px 0' }} />);
      i++; continue;
    }

    // Blockquote
    if (/^>\s/.test(line)) {
      const qlines: string[] = [];
      while (i < lines.length && /^>\s/.test(lines[i])) {
        qlines.push(lines[i].replace(/^>\s?/, ''));
        i++;
      }
      blocks.push(
        <div key={key++} style={{
          borderLeft: '3px solid var(--accent)', padding: '6px 12px', margin: '6px 0',
          background: 'var(--bg-tertiary)', borderRadius: '0 6px 6px 0',
          color: 'var(--text-secondary)', fontSize: 12,
        }}>
          {qlines.map((q, idx) => <div key={idx}>{renderInline(q, 'bq' + key + idx)}</div>)}
        </div>,
      );
      continue;
    }

    // Heading (h1-h6)
    const h = /^(#{1,6})\s+(.*)$/.exec(line);
    if (h) {
      const lv = h[1].length;
      const sizes = [18, 16, 14, 13, 12, 11];
      const weights = [700, 600, 600, 500, 500, 500];
      blocks.push(
        <div key={key++} style={{
          fontSize: sizes[lv - 1], fontWeight: weights[lv - 1],
          margin: '10px 0 4px', color: 'var(--text-primary)',
          borderBottom: lv <= 2 ? '1px solid var(--border-color)' : undefined,
          paddingBottom: lv <= 2 ? 4 : 0,
        }}>
          {renderInline(h[2], 'h' + key)}
        </div>,
      );
      i++; continue;
    }

    // Task list item (- [ ] or - [x])
    const task = /^[-*]\s+\[([ xX])\]\s+(.*)$/.exec(line);
    if (task) {
      const checked = task[1] !== ' ';
      const items: { text: string; checked: boolean }[] = [];
      while (i < lines.length) {
        const tm = /^[-*]\s+\[([ xX])\]\s+(.*)$/.exec(lines[i]);
        if (!tm) break;
        items.push({ text: tm[2], checked: tm[1] !== ' ' });
        i++;
      }
      blocks.push(
        <div key={key++} style={{ margin: '4px 0 4px 8px' }}>
          {items.map((it, k) => (
            <div key={k} style={{ display: 'flex', alignItems: 'flex-start', gap: 8, padding: '1px 0' }}>
              <span style={{
                display: 'inline-flex', alignItems: 'center', justifyContent: 'center',
                width: 16, height: 16, borderRadius: 4, flexShrink: 0, marginTop: 2,
                border: it.checked ? '1px solid var(--success)' : '1px solid var(--border-color)',
                background: it.checked ? 'var(--success)' : 'transparent',
                color: it.checked ? '#fff' : 'transparent', fontSize: 10,
              }}>
                {it.checked ? '✓' : ''}
              </span>
              <span style={{
                textDecoration: it.checked ? 'line-through' : 'none',
                color: it.checked ? 'var(--text-muted)' : 'var(--text-secondary)',
              }}>
                {renderInline(it.text, 't' + key + k)}
              </span>
            </div>
          ))}
        </div>,
      );
      continue;
    }

    // Unordered list
    if (/^[-*+]\s+/.test(line) && !/^[-*]\s+\[/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^[-*+]\s+/.test(lines[i]) && !/^[-*]\s+\[/.test(lines[i])) {
        items.push(lines[i].replace(/^[-*+]\s+/, ''));
        i++;
      }
      blocks.push(
        <ul key={key++} style={{ margin: '4px 0 4px 18px', padding: 0 }}>
          {items.map((it, k) => (
            <li key={k} style={{ marginBottom: 2 }}>{renderInline(it, 'ul' + key + k)}</li>
          ))}
        </ul>,
      );
      continue;
    }

    // Ordered list
    if (/^\d+[.)]\s+/.test(line)) {
      const items: string[] = [];
      while (i < lines.length && /^\d+[.)]\s+/.test(lines[i])) {
        items.push(lines[i].replace(/^\d+[.)]\s+/, ''));
        i++;
      }
      blocks.push(
        <ol key={key++} style={{ margin: '4px 0 4px 18px', padding: 0 }}>
          {items.map((it, k) => (
            <li key={k} style={{ marginBottom: 2 }}>{renderInline(it, 'ol' + key + k)}</li>
          ))}
        </ol>,
      );
      continue;
    }

    // Blank line
    if (trim === '') { i++; continue; }

    // Paragraph: collect consecutive non-special lines
    const para: string[] = [];
    while (i < lines.length && lines[i].trim() !== '' &&
      !lines[i].trim().startsWith('```') &&
      !/^(-{3,}|\*{3,}|_{3,})$/.test(lines[i].trim()) &&
      !/^>\s/.test(lines[i]) &&
      !/^(#{1,6})\s+/.test(lines[i]) &&
      !/^[-*+]\s+/.test(lines[i]) &&
      !/^\d+[.)]\s+/.test(lines[i]) &&
      !/^\|.+\|$/.test(lines[i].trim())
    ) {
      para.push(lines[i]); i++;
    }
    blocks.push(<div key={key++} style={{ margin: '4px 0' }}>{renderInline(para.join(' '), 'p' + key)}</div>);
  }

  return <div style={{ fontSize: 13, lineHeight: 1.7 }}>{blocks}</div>;
});

export default Markdown;
