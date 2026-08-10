// The time axis.
//
// Every span, tick, cursor and dependency edge takes its geometry from one
// scale, so nothing in the timeline can disagree about where a millisecond
// sits. Zooming is a change to the domain, not a transform applied to already
// drawn elements — which keeps label sizes and hit targets constant as the
// user zooms in.

export interface Scale {
  /** Milliseconds from T0 at the left edge. */
  readonly domainStart: number
  /** Milliseconds from T0 at the right edge. */
  readonly domainEnd: number
  /** Drawable width in CSS pixels. */
  readonly width: number

  /** Milliseconds from T0 to a pixel offset from the left edge. */
  x(ms: number): number
  /** A pixel offset back to milliseconds — for click and drag handling. */
  invert(px: number): number
  /** The width in pixels of a duration, never below `min` so a 1ms span stays visible. */
  span(fromMs: number, toMs: number, min?: number): number
}

export function linearScale(
  domainStart: number,
  domainEnd: number,
  width: number,
): Scale {
  // A zero-width domain would divide by zero; a run measured as instantaneous
  // still has to render, so give it an arbitrary 1ms.
  const extent = Math.max(1, domainEnd - domainStart)
  const perMs = width / extent

  return {
    domainStart,
    domainEnd,
    width,
    x: (ms) => (ms - domainStart) * perMs,
    invert: (px) => domainStart + px / perMs,
    span: (fromMs, toMs, min = 1) =>
      Math.max(min, (toMs - fromMs) * perMs),
  }
}

export interface Tick {
  ms: number
  x: number
  label: string
}

// Tick steps that read as round durations. Powers of ten alone would offer 1s
// then 10s with nothing between, so the 2 and 5 multiples are included, and
// the minute-scale steps are the ones a person actually says out loud.
const TICK_STEPS_MS = [
  1, 2, 5, 10, 25, 50, 100, 250, 500,
  1_000, 2_000, 5_000, 10_000, 15_000, 30_000,
  60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000, 3_600_000,
]

/**
 * Ticks across the scale's domain, spaced no closer than `minGapPx` so labels
 * cannot collide however far the axis is zoomed out.
 */
export function ticks(scale: Scale, minGapPx = 80): Tick[] {
  const extent = scale.domainEnd - scale.domainStart
  const wanted = Math.max(1, Math.floor(scale.width / minGapPx))
  const step =
    TICK_STEPS_MS.find((candidate) => extent / candidate <= wanted) ??
    TICK_STEPS_MS[TICK_STEPS_MS.length - 1]

  const out: Tick[] = []
  const first = Math.ceil(scale.domainStart / step) * step
  for (let ms = first; ms <= scale.domainEnd; ms += step) {
    out.push({ ms, x: scale.x(ms), label: formatDuration(ms) })
  }
  return out
}

/**
 * A duration as a reader would say it. Sub-second values keep milliseconds
 * because the whole finding lives at that resolution; past a minute the
 * milliseconds are noise.
 */
export function formatDuration(ms: number): string {
  if (ms < 0) return `-${formatDuration(-ms)}`
  if (ms < 1_000) return `${Math.round(ms)}ms`

  const seconds = ms / 1_000
  if (seconds < 60) {
    // 1.5s reads better than 1.500s, but 12.34s should not become 12s.
    return `${Number(seconds.toFixed(seconds < 10 ? 2 : 1))}s`
  }

  const minutes = Math.floor(seconds / 60)
  const rest = Math.round(seconds - minutes * 60)
  return rest === 0 ? `${minutes}m` : `${minutes}m${rest}s`
}
