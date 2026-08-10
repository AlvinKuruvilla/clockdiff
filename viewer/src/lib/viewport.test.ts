// The zoom window's arithmetic.
//
// These are the cases that were wrong or nearly wrong while it was being
// written: an anchor sliding out from under the pointer, a pan into an edge
// silently shrinking the window, and a run whose events all landed in the
// same millisecond dividing by zero.

import { describe, expect, it } from 'vitest'
import {
  clampViewport,
  fullExtent,
  isFullExtent,
  panBy,
  zoomAbout,
} from './viewport'

/** The fixture's extent: one service probes until 31.2s while the rest settle by 10. */
const RUN_MS = 31_200

const span = (view: { start: number; end: number }) => view.end - view.start

describe('fullExtent', () => {
  it('covers the whole run', () => {
    expect(fullExtent(RUN_MS)).toEqual({ start: 0, end: RUN_MS })
  })

  it('gives a zero-length run an axis anyway', () => {
    // Every timestamp landing on T0 is degenerate but not impossible, and a
    // zero-width domain divides by zero in the scale.
    expect(span(fullExtent(0))).toBeGreaterThan(0)
  })
})

describe('zoomAbout', () => {
  it('keeps the anchored moment under the pointer', () => {
    let view = fullExtent(RUN_MS)
    for (let i = 0; i < 12; i++) view = zoomAbout(view, 4900, 0.6, RUN_MS)

    expect(view.start).toBeLessThanOrEqual(4900)
    expect(view.end).toBeGreaterThanOrEqual(4900)
    expect(span(view)).toBeLessThan(100)
  })

  it('does not zoom past a readable window', () => {
    let view = fullExtent(RUN_MS)
    for (let i = 0; i < 200; i++) view = zoomAbout(view, 4900, 0.5, RUN_MS)

    expect(span(view)).toBeGreaterThanOrEqual(1)
  })

  it('lands back on the full extent rather than overshooting it', () => {
    let view = zoomAbout(fullExtent(RUN_MS), 4900, 0.05, RUN_MS)
    for (let i = 0; i < 40; i++) view = zoomAbout(view, 4900, 1.6, RUN_MS)

    expect(isFullExtent(view, RUN_MS)).toBe(true)
  })

  it('holds an edge when zooming in against it', () => {
    expect(zoomAbout(fullExtent(RUN_MS), 0, 0.1, RUN_MS).start).toBe(0)
    expect(zoomAbout(fullExtent(RUN_MS), RUN_MS, 0.1, RUN_MS).end).toBe(RUN_MS)
  })

  it('survives a zero-length run', () => {
    expect(span(zoomAbout(fullExtent(0), 0, 0.5, 0))).toBeGreaterThan(0)
  })
})

describe('panBy', () => {
  it('keeps its span when pushed past the start', () => {
    const view = zoomAbout(fullExtent(RUN_MS), 0, 0.1, RUN_MS)
    const panned = panBy(view, -10_000, RUN_MS)

    expect(panned.start).toBe(0)
    expect(span(panned)).toBeCloseTo(span(view))
  })

  it('keeps its span when pushed past the end', () => {
    const view = zoomAbout(fullExtent(RUN_MS), RUN_MS, 0.1, RUN_MS)
    const panned = panBy(view, 10_000, RUN_MS)

    expect(panned.end).toBe(RUN_MS)
    expect(span(panned)).toBeCloseTo(span(view))
  })
})

describe('clampViewport', () => {
  it('trims a window wider than the run', () => {
    expect(clampViewport({ start: -5_000, end: RUN_MS + 5_000 }, RUN_MS)).toEqual({
      start: 0,
      end: RUN_MS,
    })
  })

  it('widens an inverted window rather than returning one', () => {
    const view = clampViewport({ start: 900, end: 100 }, RUN_MS)
    expect(view.end).toBeGreaterThan(view.start)
  })
})
