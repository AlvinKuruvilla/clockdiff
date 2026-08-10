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

import { useCallback, useLayoutEffect, useRef, useState } from 'react'
import type { Lane, Run, Span, SpanKind } from '@/lib/run'
import { formatDuration, linearScale, ticks, type Scale } from '@/lib/scale'

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

  const scale = linearScale(0, run.durationMs, trackWidth)
  const major = trackWidth > 0 ? ticks(scale) : []

  const handleMove = useCallback(
    (event: React.MouseEvent<HTMLDivElement>) => {
      const box = event.currentTarget.getBoundingClientRect()
      setCursorMs(scale.invert(event.clientX - box.left))
    },
    [scale],
  )

  // Rendering before the first measurement would place every span at zero and
  // then jump, so hold the drawing until the width is known.
  const measured = trackWidth > 0

  return (
    <div className="flex h-full flex-col overflow-hidden bg-background">
      <div
        className="relative flex shrink-0 border-b"
        onMouseLeave={() => setCursorMs(null)}
        onMouseMove={handleMove}
      >
        <div
          className="shrink-0 border-r px-2 py-1 text-[10px] tracking-wide text-muted-foreground uppercase"
          style={{ width: GUTTER_WIDTH, height: RULER_HEIGHT }}
        >
          service
        </div>
        <Ruler scale={scale} major={major} measured={measured} />
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

        <div className="relative flex-1" ref={trackRef}>
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
