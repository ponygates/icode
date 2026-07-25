import React from 'react';

interface State {
  error: Error | null;
}

// ErrorBoundary catches render-time exceptions anywhere in the tree. Without
// it, a thrown error during render unmounts the whole React root and leaves a
// blank / apparently "frozen" window. Here we surface the error (and the
// stack) so the failure is diagnosable instead of silent.
class ErrorBoundary extends React.Component<{ children: React.ReactNode }, State> {
  constructor(props: { children: React.ReactNode }) {
    super(props);
    this.state = { error: null };
  }

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: React.ErrorInfo) {
    // eslint-disable-next-line no-console
    console.error('[iCode] render error:', error, info.componentStack);
    try {
      localStorage.setItem(
        'icode.lastError',
        JSON.stringify({ message: error.message, stack: error.stack, at: Date.now() })
      );
    } catch {
      /* ignore */
    }
  }

  render() {
    if (this.state.error) {
      return (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            zIndex: 99999,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            background: 'var(--bg-primary)',
            color: 'var(--text-primary)',
            padding: 24,
          }}
        >
          <div
            style={{
              maxWidth: 640,
              width: '100%',
              background: 'var(--bg-secondary)',
              border: '1px solid var(--border-color)',
              borderRadius: 12,
              padding: 24,
            }}
          >
            <h2 style={{ marginTop: 0, fontSize: 18 }}>界面渲染出错</h2>
            <p style={{ color: 'var(--text-secondary)', fontSize: 13, lineHeight: 1.6 }}>
              应用遇到一个界面错误（已记录到控制台与 <code>localStorage.icode.lastError</code>）。
              请复制下方错误信息反馈，或重启应用后重试。
            </p>
            <pre
              style={{
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-word',
                fontSize: 12,
                background: 'var(--bg-primary)',
                border: '1px solid var(--border-color)',
                borderRadius: 8,
                padding: 12,
                maxHeight: 320,
                overflow: 'auto',
                color: 'var(--text-muted)',
              }}
            >
              {this.state.error.message}
              {'\n\n'}
              {this.state.error.stack}
            </pre>
            <button
              onClick={() => this.setState({ error: null })}
              style={{
                marginTop: 12,
                padding: '8px 16px',
                borderRadius: 8,
                border: 'none',
                background: 'var(--accent)',
                color: '#fff',
                cursor: 'pointer',
                fontSize: 13,
              }}
            >
              重试
            </button>
          </div>
        </div>
      );
    }
    return this.props.children;
  }
}

export default ErrorBoundary;
