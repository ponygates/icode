// Pure message-list windowing logic (dependency-free virtualisation).
//
// WHY custom rather than a virtualiser library: chat rows have wildly
// variable heights that change *while scrolling* (streaming appends to the
// last row; code blocks collapse/expand above the viewport). The requirements
// we must hit — keep the DOM node count bounded (not just paint-skip via
// content-visibility), preserve the scroll anchor when content above resizes,
// and stick-to-bottom while the last item grows — are simpler and more fully
// under our control with a small item-height cache + measured-range window
// than by fighting an opaque measureElement implementation. The window math
// lives here so it can be unit-tested without a DOM.
//
// Model: we cache each message's measured height, compute cumulative offsets,
// and render only the [first,last] slice intersecting the viewport (+overscan),
// padding with top/bottom spacers so the scrollbar length and anchor stay
// correct. Off-screen rows are not mounted, so DOM nodes stay bounded by the
// window size instead of the session length.

export interface WindowOpts {
  /** Total number of messages in the session. */
  count: number;
  /** Container scrollTop (px from the top of the scrolled content). */
  scrollTop: number;
  /** Visible viewport height in px. */
  viewport: number;
  /** Fallback height for a message that has never been measured. */
  estimate: number;
  /** Extra rows rendered above/below the viewport to avoid blank flashes. */
  overscan: number;
}

export interface MessageWindow {
  /** First/last rendered index (inclusive); both are -1 when there is nothing. */
  first: number;
  last: number;
  /** Height of the spacer above the rendered slice (== cumulative offset of `first`). */
  padTop: number;
  /** Height of the spacer below the rendered slice. */
  padBottom: number;
  /** Full scrollable height (sum of every cached/estimated row). */
  totalHeight: number;
}

// Sum of cached/estimated heights for rows in [0, index). A missing entry falls
// back to the estimate so the offsets never leave a "hole" before measurement.
export function cumBefore(
  heights: ArrayLike<number>,
  index: number,
  estimate: number
): number {
  let sum = 0;
  const n = Math.min(index, heights.length);
  for (let i = 0; i < n; i++) sum += heights[i] || estimate;
  // Rows beyond what we've cached yet still contribute the estimate so the
  // scrollbar doesn't jump as new messages stream in.
  for (let i = heights.length; i < index; i++) sum += estimate;
  return sum;
}

// Binary search for the first row whose bottom edge reaches `targetOffset`.
function findRowAt(heights: ArrayLike<number>, estimate: number, count: number, targetOffset: number): number {
  if (count <= 0) return 0;
  let lo = 0;
  let hi = count - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (cumBefore(heights, mid + 1, estimate) <= targetOffset) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

// Compute the visible slice + spacers for the current scroll position.
export function computeWindow(
  heights: ArrayLike<number>,
  opts: WindowOpts
): MessageWindow {
  const { count, scrollTop, viewport, estimate, overscan } = opts;
  if (count === 0 || viewport <= 0) {
    return { first: 0, last: -1, padTop: 0, padBottom: 0, totalHeight: 0 };
  }
  const totalHeight = cumBefore(heights, count, estimate);
  const scrollBottom = scrollTop + viewport;

  let first = findRowAt(heights, estimate, count, Math.max(0, scrollTop));
  first = Math.max(0, first - overscan);

  let last = findRowAt(heights, estimate, count, Math.max(0, scrollBottom));
  last = Math.min(count - 1, last + overscan);
  if (last < first) last = first;

  const padTop = cumBefore(heights, first, estimate);
  const renderedHeight = cumBefore(heights, last + 1, estimate) - padTop;
  const padBottom = Math.max(0, totalHeight - padTop - renderedHeight);
  return { first, last, padTop, padBottom, totalHeight };
}

// Distance (px) between the bottom of the content and the bottom of the
// viewport. <=0 means already scrolled to the very bottom.
export function distanceToBottom(
  scrollHeight: number,
  scrollTop: number,
  clientHeight: number
): number {
  return scrollHeight - scrollTop - clientHeight;
}

// Stick-to-bottom / "jump to latest" threshold, shared by the scroll handler
// and the bottom sentinel so both agree on what "at the bottom" means.
export const BOTTOM_STICK_PX = 80;

// Default row height hint (before measurement) and how many rows to render
// beyond the viewport edges. Tuned for chat bubbles (~140px intrinsic size the
// old content-visibility hint used) with a small overscan to avoid blank
// flashes during fast wheel/key scrolling.
export const ROW_ESTIMATE = 140;
export const ROW_OVERSCAN = 6;

export function isNearBottom(distanceFromBottom: number): boolean {
  return distanceFromBottom <= BOTTOM_STICK_PX;
}

export interface Anchor {
  /** Index of the first fully-visible row (the row we pin the viewport to). */
  index: number;
  /** Its top edge relative to the container top, captured before a resize. */
  offsetTop: number;
}

// What scrollTop restores the anchor after content above it resized (e.g. a
// code block collapsed). `offsetNowTop` is the anchor row's live offsetTop as
// measured after layout. Pure so the compensation is testable without a DOM.
export function computeAnchorScrollTop(
  currentScrollTop: number,
  anchor: Anchor,
  offsetNowTop: number
): number {
  const delta = offsetNowTop - anchor.offsetTop;
  const next = currentScrollTop + delta;
  return next < 0 ? 0 : next;
}

// Whether we should honour an anchor correction at all: only when the anchor
// row sits near the top of the viewport (a row deep below the fold resizing
// shouldn't yank the scroll position).
export function shouldApplyAnchor(anchor: Anchor, viewport: number): boolean {
  return anchor.offsetTop >= -BOTTOM_STICK_PX && anchor.offsetTop <= viewport * 0.5;
}
