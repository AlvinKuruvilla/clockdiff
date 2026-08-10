// The lane timeline.
//
// Spans are absolutely-positioned divs rather than SVG rects: at the scale of
// a compose stack the element count is trivial, and the DOM gives hit-testing,
// focus rings, keyboard navigation and text truncation without reimplementing
// any of them. SVG is kept for the dependency edges, which are the one thing
// here that genuinely needs path geometry.
//
// Nothing computes its own geometry. Every left and width on this page comes
// from the one `Scale` passed down, so a span, its tick and the cursor cannot
// disagree about where a millisecond sits.
//
// The layout is deliberately dense. A profiler is read by scanning rows for
// the one that is wrong, and rows that need vertical eye travel defeat that.

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import {
  conditionMetAt,
  type Condition,
  type Lane,
  type Run,
  type Span,
  type SpanKind,
} from '@/lib/run'
import { formatDuration, linearScale, ticks, type Scale } from '@/lib/scale'
import {
  fullExtent,
  isFullExtent,
  panBy,
  zoomAbout,
  type Viewport,
} from '@/lib/viewport'

const LANE_HEIGHT = 18
const LANE_GAP = 2
const GUTTER_WIDTH = 150
const RULER_HEIGHT = 26
const MINOR_TICKS_PER_MAJOR = 5

const SPAN_FILL: Record<SpanKind, string> = {
  create: 'var(--span-create)',
  wait: 'var(--span-wait)',
  boot: 'var(--span-boot)',
  dead: 'var(--span-dead)',
  probing: 'var(--span-probing)',
  ran: 'var(--span-ran)',
  done: 'var(--span-done)',
  crashed: 'var(--span-crashed)',
}

const SPAN_TITLE: Record<SpanKind, string> = {
  create: 'container created, nothing gating it',
  wait: 'held by depends_on',
  boot: 'starting up',
  dead: 'ready, but nothing had noticed yet',
  probing: 'probed and never passed',
  ran: 'running',
  done: 'exited cleanly',
  crashed: 'exited non-zero',
}

// Ordered as a startup reads, left to right, so the legend doubles as an
// explanation of what the lanes are showing.
const LEGEND: SpanKind[] = [
  'create',
  'wait',
  'boot',
  'dead',
  'ran',
  'probing',
  'done',
  'crashed',
]

/** The vertical centre of a lane, by its position in the list. */
function laneCentre(index: number): number {
  return index * (LANE_HEIGHT + LANE_GAP) + LANE_HEIGHT / 2
}

interface Edge {
  from: { ms: number; row: number }
  to: { ms: number; row: number }
  label: string
  /** True when this is an edge the selected service was itself waiting on. */
  inbound: boolean
}

/**
 * The `depends_on` edges touching one service, in both directions.
 *
 * Only the selected service's edges are drawn. A stack in the corpus reaches
 * 58 edges, and all of them at once is a mesh nobody can read; one service's
 * are the answer to a question somebody just asked by clicking.
 *
 * An edge runs from the moment a condition was satisfied to the moment the
 * waiting service started, and its length reads the opposite way round to
 * what one expects: the shortest inbound edge is the binding one. A long edge
 * means that dependency was ready well before the service could start and so
 * was never what held it; the edge that barely spans anything is the gate
 * that had just cleared.
 *
 * Which edge that is, is left to the reader — see docs/design/001, where a
 * critical-path solver is declined on the grounds that a compose graph is
 * about three levels deep and the path is plain once the structure is drawn.
 */
function edgesTouching(lanes: Lane[], selected: string | null): Edge[] {
  if (selected === null) return []

  const row = new Map(lanes.map((lane, index) => [lane.name, index]))
  const byName = new Map(lanes.map((lane) => [lane.name, lane]))
  const out: Edge[] = []

  const add = (
    waiter: Lane,
    dependency: Lane,
    condition: Condition,
    inbound: boolean,
  ) => {
    const met = conditionMetAt(dependency, condition)
    const started = waiter.moments.started
    const fromRow = row.get(dependency.name)
    const toRow = row.get(waiter.name)
    if (met === null || started === null || fromRow === undefined || toRow === undefined) {
      return
    }
    out.push({
      from: { ms: met, row: fromRow },
      to: { ms: started, row: toRow },
      label: `${waiter.name} waited for ${dependency.name} (${condition})`,
      inbound,
    })
  }

  const chosen = byName.get(selected)
  if (chosen === undefined) return []

  // What it waited on.
  for (const dep of chosen.dependsOn) {
    const dependency = byName.get(dep.service)
    if (dependency !== undefined) add(chosen, dependency, dep.condition, true)
  }

  // Whose start it pushed out. This is the direction a per-service table
  // cannot show, and the reason a dead-time figure is worth more than the row
  // it appears on.
  for (const lane of lanes) {
    if (lane.name === selected) continue
    for (const dep of lane.dependsOn) {
      if (dep.service === selected) add(lane, chosen, dep.condition, false)
    }
  }

  return out
}

/** Tracks an element's content width so the scale is built from real pixels. */
function useMeasuredWidth(): [React.RefObject<HTMLDivElement | null>, number] {
  const ref = useRef<HTMLDivElement>(null)
  const [width, setWidth] = useState(0)

  useLayoutEffect(() => {
    const element = ref.current
    if (element === null) return

    const observer = new ResizeObserver(([entry]) => {
      setWidth(entry.contentRect.width)
    })
    observer.observe(element)
    return () => observer.disconnect()
  }, [])

  return [ref, width]
}

export interface TimelineProps {
  run: Run
  selected: string | null
  onSelect: (name: string | null) => void
}

export function Timeline({ run, selected, onSelect }: TimelineProps) {
  const [trackRef, trackWidth] = useMeasuredWidth()
  const [cursorMs, setCursorMs] = useState<number | null>(null)
  const [view, setView] = useState<Viewport>(() => fullExtent(run.durationMs))
  const [drag, setDrag] = useState<{ from: number; to: number } | null>(null)

  // A different run is a different axis; keeping the old window would show a
  // slice of a run that no longer exists.
  useEffect(() => setView(fullExtent(run.durationMs)), [run])

  const scale = linearScale(view.start, view.end, trackWidth)
  const major = trackWidth > 0 ? ticks(scale) : []

  // Positions are measured against the track, never against a row that also
  // contains the gutter — the gutter's width would otherwise be read as time.
  const pointerMs = useCallback(
    (clientX: number): number | null => {
      const track = trackRef.current
      if (track === null) return null
      return scale.invert(clientX - track.getBoundingClientRect().left)
    },
    [scale, trackRef],
  )

  const handleMove = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      setCursorMs(pointerMs(event.clientX))
    },
    [pointerMs],
  )

  // Wheel is registered natively and non-passively: React's synthetic wheel
  // listener is passive, so preventDefault on it is ignored and the page
  // scrolls while zooming.
  const surfaceRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const surface = surfaceRef.current
    if (surface === null) return

    const onWheel = (event: WheelEvent) => {
      const at = pointerMs(event.clientX)
      if (at === null) return
      event.preventDefault()

      if (event.shiftKey) {
        const perPixel = (view.end - view.start) / Math.max(1, trackWidth)
        setView(panBy(view, event.deltaY * perPixel, run.durationMs))
        return
      }
      const factor = Math.exp(event.deltaY * 0.002)
      setView(zoomAbout(view, at, factor, run.durationMs))
    }

    surface.addEventListener('wheel', onWheel, { passive: false })
    return () => surface.removeEventListener('wheel', onWheel)
  }, [pointerMs, run.durationMs, trackWidth, view])

  // Dragging across the ruler picks a range. The window is only committed on
  // release, so a drag can be abandoned by ending it where it started.
  const beginDrag = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      const at = pointerMs(event.clientX)
      if (at === null) return
      event.preventDefault()
      setDrag({ from: at, to: at })

      const onMove = (move: MouseEvent) => {
        const to = pointerMs(move.clientX)
        if (to !== null) setDrag((current) => (current ? { ...current, to } : null))
      }
      const onUp = () => {
        window.removeEventListener('mousemove', onMove)
        window.removeEventListener('mouseup', onUp)
        setDrag((current) => {
          if (current !== null) {
            const start = Math.min(current.from, current.to)
            const end = Math.max(current.from, current.to)
            // A click, not a drag. Leave the window alone.
            if (end - start > 0) setView({ start, end })
          }
          return null
        })
      }
      window.addEventListener('mousemove', onMove)
      window.addEventListener('mouseup', onUp)
    },
    [pointerMs],
  )

  const zoomed = !isFullExtent(view, run.durationMs)

  // Rendering before the first measurement would place every span at zero and
  // then jump, so hold the drawing until the width is known.
  const measured = trackWidth > 0

  return (
    <div
      className="flex h-full flex-col overflow-hidden bg-background"
      ref={surfaceRef}
    >
      <div
        className="relative flex shrink-0 border-b"
        onMouseLeave={() => setCursorMs(null)}
        onMouseMove={handleMove}
      >
        <div
          className="flex shrink-0 items-center justify-between border-r px-2 text-[10px] tracking-wide text-muted-foreground uppercase"
          style={{ width: GUTTER_WIDTH, height: RULER_HEIGHT }}
        >
          <span>service</span>
          {zoomed && (
            <button
              className="rounded border px-1 text-[9px] normal-case hover:text-foreground"
              onClick={() => setView(fullExtent(run.durationMs))}
              title="show the whole run"
              type="button"
            >
              reset
            </button>
          )}
        </div>
        <div className="relative flex-1 cursor-ew-resize" onMouseDown={beginDrag}>
          <Ruler scale={scale} major={major} measured={measured} />
        </div>
      </div>

      <div
        className="relative flex min-h-0 flex-1 overflow-y-auto"
        onMouseLeave={() => setCursorMs(null)}
        onMouseMove={handleMove}
      >
        <div className="shrink-0 border-r" style={{ width: GUTTER_WIDTH }}>
          {run.lanes.map((lane) => (
            <LaneLabel
              key={lane.name}
              lane={lane}
              selected={lane.name === selected}
              onSelect={onSelect}
            />
          ))}
        </div>

        {/* Clipped, because a zoomed-in span is positioned far outside the
            track and must not paint over the gutter. */}
        <div className="relative flex-1 overflow-hidden" ref={trackRef}>
          {/* Rules sit behind the spans so a span edge can be read against
              the axis without the rule cutting through it. */}
          {major.map((tick) => (
            <div
              key={tick.ms}
              className="absolute top-0 bottom-0 w-px bg-border/60"
              style={{ left: tick.x }}
            />
          ))}

          {measured &&
            run.lanes.map((lane) => (
              <LaneTrack
                key={lane.name}
                lane={lane}
                scale={scale}
                selected={lane.name === selected}
                onSelect={onSelect}
              />
            ))}

          {measured && (
            <EdgeLayer
              edges={edgesTouching(run.lanes, selected)}
              scale={scale}
              height={run.lanes.length * (LANE_HEIGHT + LANE_GAP)}
            />
          )}

          {measured && drag !== null && (
            <div
              className="pointer-events-none absolute top-0 bottom-0 z-10 border-x border-foreground/40 bg-foreground/10"
              style={{
                left: scale.x(Math.min(drag.from, drag.to)),
                width: Math.abs(scale.x(drag.to) - scale.x(drag.from)),
              }}
            />
          )}

          {measured && cursorMs !== null && (
            <div
              className="pointer-events-none absolute top-0 bottom-0 z-10 w-px bg-foreground/50"
              style={{ left: scale.x(cursorMs) }}
            />
          )}
        </div>
      </div>

      <div
        className="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-t px-2 py-1.5 text-[10px] text-muted-foreground"
      >
        {LEGEND.map((kind) => (
          <span key={kind} className="flex items-center gap-1">
            <span
              className="inline-block h-2 w-2 rounded-[1px]"
              style={
                kind === 'probing'
                  ? {
                      backgroundColor: 'var(--muted)',
                      backgroundImage: `repeating-linear-gradient(45deg, ${SPAN_FILL.probing} 0 3px, transparent 3px 6px)`,
                    }
                  : { background: SPAN_FILL[kind] }
              }
            />
            {kind === 'dead' ? (
              <span className="font-medium text-foreground">
                dead — recoverable
              </span>
            ) : (
              SPAN_TITLE[kind]
            )}
          </span>
        ))}

        <span className="ml-auto flex items-center gap-3 tabular-nums">
          {cursorMs !== null && <span>{formatDuration(cursorMs)}</span>}
          <span className="opacity-70">
            {zoomed
              ? `${formatDuration(view.start)}–${formatDuration(view.end)}`
              : 'drag the ruler to zoom · wheel to scale · shift-wheel to pan'}
          </span>
        </span>
      </div>
    </div>
  )
}

/**
 * The ruler. Minor ticks subdivide each labelled step, which is what lets a
 * reader estimate a span's extent without dragging the cursor to it.
 */
function Ruler({
  scale,
  major,
  measured,
}: {
  scale: Scale
  major: ReturnType<typeof ticks>
  measured: boolean
}) {
  if (!measured || major.length < 2) {
    return <div className="relative flex-1" style={{ height: RULER_HEIGHT }} />
  }

  const step = major[1].ms - major[0].ms
  const minor: number[] = []
  for (const tick of major) {
    for (let i = 1; i < MINOR_TICKS_PER_MAJOR; i++) {
      const ms = tick.ms + (step * i) / MINOR_TICKS_PER_MAJOR
      if (ms < scale.domainEnd) minor.push(ms)
    }
  }

  return (
    <div className="relative flex-1" style={{ height: RULER_HEIGHT }}>
      {minor.map((ms) => (
        <div
          key={ms}
          className="absolute bottom-0 h-1 w-px bg-border"
          style={{ left: scale.x(ms) }}
        />
      ))}
      {major.map((tick) => (
        <div key={tick.ms}>
          <div
            className="absolute bottom-0 h-2 w-px bg-muted-foreground/60"
            style={{ left: tick.x }}
          />
          <span
            className="absolute top-1 pl-1 font-mono text-[10px] text-muted-foreground"
            style={{ left: tick.x }}
          >
            {tick.label}
          </span>
        </div>
      ))}
    </div>
  )
}

/**
 * The dependency edges, and the only part of the timeline drawn in SVG.
 *
 * Everything else is a rectangle a div does better; a curve between two
 * arbitrary points is the one thing that genuinely needs path geometry.
 */
function EdgeLayer({
  edges,
  scale,
  height,
}: {
  edges: Edge[]
  scale: Scale
  height: number
}) {
  if (edges.length === 0) return null

  return (
    <svg
      className="pointer-events-none absolute inset-0 z-10 h-full w-full overflow-visible"
      aria-hidden="true"
    >
      <defs>
        <marker
          id="edge-arrow"
          markerWidth="5"
          markerHeight="5"
          refX="4"
          refY="2.5"
          orient="auto"
        >
          <path d="M0,0 L5,2.5 L0,5 z" fill="currentColor" />
        </marker>
      </defs>
      {edges.map((edge, index) => {
        const x1 = scale.x(edge.from.ms)
        const y1 = laneCentre(edge.from.row)
        const x2 = scale.x(edge.to.ms)
        const y2 = laneCentre(edge.to.row)

        // Control points pushed out horizontally rather than vertically, so
        // the curve leaves the moment it depends on travelling along the time
        // axis and cannot be mistaken for a span.
        const bend = Math.max(12, Math.min(60, Math.abs(x2 - x1) / 2))

        return (
          <path
            key={index}
            d={`M${x1},${y1} C${x1 + bend},${y1} ${x2 - bend},${y2} ${x2},${y2}`}
            fill="none"
            stroke="currentColor"
            strokeWidth={1}
            strokeDasharray={edge.inbound ? undefined : '3 2'}
            markerEnd="url(#edge-arrow)"
            className={
              edge.inbound ? 'text-foreground/70' : 'text-muted-foreground/50'
            }
          >
            <title>{edge.label}</title>
          </path>
        )
      })}
      <rect width="0" height={height} />
    </svg>
  )
}

function LaneLabel({
  lane,
  selected,
  onSelect,
}: {
  lane: Lane
  selected: boolean
  onSelect: (name: string | null) => void
}) {
  return (
    <button
      className={`flex w-full items-center justify-between gap-2 px-2 text-left font-mono text-[11px] ${
        selected
          ? 'bg-accent text-foreground'
          : 'text-muted-foreground hover:bg-accent/40'
      }`}
      style={{ height: LANE_HEIGHT, marginBottom: LANE_GAP }}
      onClick={() => onSelect(selected ? null : lane.name)}
      type="button"
    >
      <span className="truncate">{lane.name}</span>
      {lane.deadMs !== null && lane.deadMs > 0 && (
        <span
          className="shrink-0 tabular-nums"
          style={{ color: 'var(--span-dead)' }}
          title="recoverable: ready this long before anything noticed"
        >
          {formatDuration(lane.deadMs)}
        </span>
      )}
    </button>
  )
}

function LaneTrack({
  lane,
  scale,
  selected,
  onSelect,
}: {
  lane: Lane
  scale: Scale
  selected: boolean
  onSelect: (name: string | null) => void
}) {
  return (
    <div
      className={`relative ${selected ? 'bg-accent' : 'hover:bg-accent/40'}`}
      style={{ height: LANE_HEIGHT, marginBottom: LANE_GAP }}
      onClick={() => onSelect(selected ? null : lane.name)}
    >
      {lane.spans.map((span, index) => (
        <SpanBar key={index} span={span} scale={scale} />
      ))}
    </div>
  )
}

function SpanBar({ span, scale }: { span: Span; scale: Scale }) {
  const open = span.to === null
  const end = span.to ?? scale.domainEnd
  const left = scale.x(span.from)
  const width = scale.span(span.from, end, 2)

  // An open span never ended, so it is drawn fading out rather than with a
  // right edge — a hard edge would assert a moment that was never observed.
  const style: React.CSSProperties = {
    left,
    width,
    top: 3,
    bottom: 3,
    background: open
      ? `linear-gradient(to right, ${SPAN_FILL[span.kind]}, transparent)`
      : SPAN_FILL[span.kind],
  }

  // Probing time could not have been saved. Striping it keeps it apart from
  // dead time for a reader who cannot rely on hue.
  if (span.kind === 'probing') {
    style.backgroundImage = `repeating-linear-gradient(45deg, ${SPAN_FILL.probing} 0 5px, transparent 5px 10px)`
    style.backgroundColor = 'var(--muted)'
  }

  const label = `${SPAN_TITLE[span.kind]} — ${formatDuration(span.from)} to ${
    open ? 'never' : formatDuration(end)
  }`

  return (
    <div
      className="absolute rounded-[1px]"
      style={style}
      title={label}
      aria-label={label}
    />
  )
}
