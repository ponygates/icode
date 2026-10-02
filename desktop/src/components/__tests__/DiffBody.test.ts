import { describe, it, expect } from 'vitest';
import { parseUnifiedDiff, diffColorFor } from '../DiffBody';

const DIFF = [
  'diff --git a/foo.ts b/foo.ts',
  'index 111..222 100644',
  '--- a/foo.ts',
  '+++ b/foo.ts',
  '@@ -1,3 +1,4 @@',
  ' line one',
  '-line two',
  '+line two changed',
  '+line three new',
  ' line four',
  'diff --git a/bar.ts b/bar.ts',
  '--- a/bar.ts',
  '+++ b/bar.ts',
  '@@ -1 +1 @@',
  '-old',
  '+new',
].join('\n');

describe('parseUnifiedDiff', () => {
  it('splits git diff output into one file section per `diff --git` header', () => {
    const files = parseUnifiedDiff(DIFF);
    expect(files).toHaveLength(2);
    expect(files[0].header).toBe('a/foo.ts b/foo.ts');
    expect(files[1].header).toBe('a/bar.ts b/bar.ts');
  });

  it('classifies added / removed / context / hunk / meta lines', () => {
    const kinds = parseUnifiedDiff(DIFF)[0].lines.map((l) => l.kind);
    expect(kinds).toEqual([
      'meta',   // +++
      'hunk',   // @@
      'ctx',    //  line one
      'del',    // -line two
      'add',    // +line two changed
      'add',    // +line three new
      'ctx',    //  line four
    ]);
  });

  it('preserves the raw line text for rendering', () => {
    const lines = parseUnifiedDiff(DIFF)[0].lines;
    const added = lines.find((l) => l.kind === 'add');
    expect(added?.text).toBe('+line two changed');
  });

  it('handles an empty diff without throwing', () => {
    expect(parseUnifiedDiff('')).toHaveLength(1);
    // a lone empty string produces a single headerless file with one ctx line
    expect(parseUnifiedDiff('')[0].header).toBe('');
  });

  it('colours map to semantic kinds', () => {
    expect(diffColorFor('add').fg).toBe('var(--success)');
    expect(diffColorFor('del').fg).toBe('var(--error)');
    expect(diffColorFor('hunk').fg).toBe('var(--accent)');
  });
});
