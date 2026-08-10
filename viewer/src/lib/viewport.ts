// The visible slice of the time axis.
//
// A run's extent is set by whatever finished last, and that is routinely a
// service which never became ready at all — one healthcheck probing until it
// times out puts the axis at 31s while everything real settled inside 10.
// Fitting the axis to the interesting part instead would mean deciding which
// part is interesting, and hiding whatever did not qualify. So the default
// stays honest — the whole run — and zooming is how the detail is reached,
// which is what every profiler with this problem already does.

export interface Viewport {
  start: number
  end: number
}

/** Below this the axis is all rounding error, and labels stop being useful. */
const MIN_SPAN_MS = 1

/**
 * Clamps a viewport back inside the run and enforces the minimum span,
 * keeping the centre where the caller put it rather than sliding it.
 */
export function clampViewport(view: Viewport, durationMs: number): Viewport {
  const limit = Math.max(MIN_SPAN_MS, durationMs)
  let { start, end } = view

  if (end - start < MIN_SPAN_MS) {
    const centre = (start + end) / 2
    start = centre - MIN_SPAN_MS / 2
    end = centre + MIN_SPAN_MS / 2
  }

  // Shift before trimming, so zooming out near an edge widens the far side
  // rather than pinning and losing span.
  const span = Math.min(end - start, limit)
  if (start < 0) {
    start = 0
    end = span
  }
  if (end > limit) {
    end = limit
    start = limit - span
  }

  return { start: Math.max(0, start), end: Math.min(limit, end) }
}

/**
 * Zooms by `factor` about `anchorMs`, so the millisecond under the pointer
 * stays under the pointer. Factor below 1 zooms in.
 */
export function zoomAbout(
  view: Viewport,
  anchorMs: number,
  factor: number,
  durationMs: number,
): Viewport {
  return clampViewport(
    {
      start: anchorMs - (anchorMs - view.start) * factor,
      end: anchorMs + (view.end - anchorMs) * factor,
    },
    durationMs,
  )
}

/** Slides the viewport by a duration, without changing its span. */
export function panBy(
  view: Viewport,
  deltaMs: number,
  durationMs: number,
): Viewport {
  return clampViewport(
    { start: view.start + deltaMs, end: view.end + deltaMs },
    durationMs,
  )
}

export function fullExtent(durationMs: number): Viewport {
  return { start: 0, end: Math.max(MIN_SPAN_MS, durationMs) }
}

export function isFullExtent(view: Viewport, durationMs: number): boolean {
  const full = fullExtent(durationMs)
  return view.start === full.start && view.end === full.end
}
