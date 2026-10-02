import { describe, it, expect } from 'vitest';
import {
  computeWindow,
  cumBefore,
  distanceToBottom,
  isNearBottom,
  computeAnchorScrollTop,
  shouldApplyAnchor,
} from '../messageWindow';

const H = (arr: number[]) => arr;

describe('computeWindow', () => {
  const base = { overscan: 2, estimate: 100 };

  it('returns an empty window when there is nothing to render', () => {
    const w = computeWindow(H([]), { count: 0, scrollTop: 0, viewport: 400, ...base });
    expect(w).toEqual({ first: 0, last: -1, padTop: 0, padBottom: 0, totalHeight: 0 });
  });

  it('windows the middle of a long list, mounting only ~viewport rows + overscan', () => {
    // 100 rows of 50px each = 5000px total; viewport 200px starting at scrollTop 1000.
    const heights = H(Array(100).fill(50));
    const w = computeWindow(heights, { count: 100, scrollTop: 1000, viewport: 200, ...base });
    // row 20 starts at 1000, row 23 ends at 1200 -> visible ~[20..24]; ±overscan 2
    expect(w.first).toBe(18);
    expect(w.last).toBe(26);
    expect(w.padTop).toBe(18 * 50);
    expect(w.totalHeight).toBe(100 * 50);
    // spacers must account for everything not mounted
    expect(w.padTop + w.padBottom).toBe(w.totalHeight - (w.last - w.first + 1) * 50);
  });

  it('clamps first/last into [0, count-1]', () => {
    const heights = H(Array(3).fill(80));
    const w = computeWindow(heights, { count: 3, scrollTop: 0, viewport: 1000, ...base });
    expect(w.first).toBe(0);
    expect(w.last).toBe(2);
    expect(w.padTop).toBe(0);
    expect(w.padBottom).toBe(0);
  });

  it('uses the estimate for unmeasured rows so the scrollbar does not collapse', () => {
    // Only the first two rows measured; rest fall back to estimate 100.
    const heights = H([200, 300]);
    const total = cumBefore(heights, 10, 100);
    expect(total).toBe(200 + 300 + 8 * 100);
    const w = computeWindow(heights, { count: 10, scrollTop: 0, viewport: 10000, overscan: 0, estimate: 100 });
    expect(w.totalHeight).toBe(total);
  });

  it('anchors to the bottom row when scrolled to the end of a growing stream', () => {
    const heights = H(Array(50).fill(40)); // last row keeps growing in practice
    const total = 50 * 40;
    const w = computeWindow(heights, { count: 50, scrollTop: total - 200, viewport: 200, overscan: 2, estimate: 40 });
    expect(w.last).toBe(49);
    expect(w.padBottom).toBe(0);
  });
});

describe('near-bottom detection', () => {
  it('distanceToBottom reports px remaining to the content bottom', () => {
    expect(distanceToBottom(1000, 800, 200)).toBe(0);
    expect(distanceToBottom(1000, 500, 200)).toBe(300);
  });
  it('isNearBottom honours the shared stick threshold', () => {
    expect(isNearBottom(0)).toBe(true);
    expect(isNearBottom(80)).toBe(true);
    expect(isNearBottom(81)).toBe(false);
  });
});

describe('anchor preservation', () => {
  it('scrolls by exactly the delta a row above the viewport grew/shrank', () => {
    // Anchor row 5 sat 10px under the container top; after a code block above
    // collapsed it is now -200px (moved up 210) -> we must scroll up 210 to keep
    // the same visual line pinned.
    const next = computeAnchorScrollTop(1000, { index: 5, offsetTop: 10 }, -200);
    expect(next).toBe(1000 + (-200 - 10));
  });

  it('never produces a negative scrollTop', () => {
    // anchor moves up 100px but we only had 5px of scroll above -> clamp to 0.
    expect(computeAnchorScrollTop(5, { index: 0, offsetTop: 100 }, 0)).toBe(0);
  });

  it('only applies the correction when the anchor sits near the top of the viewport', () => {
    expect(shouldApplyAnchor({ index: 3, offsetTop: 40 }, 600)).toBe(true);
    expect(shouldApplyAnchor({ index: 3, offsetTop: -30 }, 600)).toBe(true); // just scrolled off top
    expect(shouldApplyAnchor({ index: 3, offsetTop: 500 }, 600)).toBe(false); // deep in viewport, don't yank
  });
});
