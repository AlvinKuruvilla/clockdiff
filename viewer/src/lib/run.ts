// The run file as clockdiff writes it, and the timeline model derived from it.
//
// The wire types mirror `report.Document` field for field; renaming anything
// here means the Go side changed and `report.FormatVersion` was bumped. The
// derived types below are the viewer's own, and exist because the file stores
// only recorded moments — every span the timeline draws is a difference
// between two of them, computed here rather than read.

/** Bump in step with `report.FormatVersion`. */
export const SUPPORTED_FORMAT_VERSION = 1

/** Outcome strings are format, not debug output. See `runtime.Outcome`. */
export type Outcome =
  | 'pending'
  | 'healthy'
  | 'unhealthy'
  | 'crashed'
  | 'completed'
  | 'accepting'
  | 'no-readiness'

export type Condition =
  | 'service_healthy'
  | 'service_started'
  | 'service_completed_successfully'

export interface WireDependency {
  service: string
  condition: Condition
}

export interface WireService {
  name: string
  outcome: Outcome

  created?: string
  started?: string
  predicateTrue?: string
  declaredHealthy?: string
  declaredUnhealthy?: string
  accepting?: string
  exited?: string

  hasHealthcheck: boolean
  expectsPort: boolean

  exitCode?: number
  crashLog?: string[]
  dependsOn?: WireDependency[]
}

export interface WireDocument {
  version: number
  project: string
  startedAt: string
  services: WireService[]
  alreadyRunning?: string[]
}

// What a lane is made of.
//
// `wait` and `dead` are the two spans a reader is looking for, and they are
// not the same kind of claim. Dead time is waste — the service was ready and
// nothing knew. A wait is correct by construction: the service was gated and
// the gate had not cleared. Keeping them as separate kinds is what stops the
// viewer from summing them into a single misleading "lost" number.
export type SpanKind =
  | 'create' // container created, not yet started, nothing gating it
  | 'wait' // created and held by depends_on until its gates cleared
  | 'boot' // starting up, genuinely working
  | 'dead' // ready, but nothing had noticed yet
  | 'probing' // healthchecked and never passed; no claim available
  | 'ran' // no readiness signal at all, just running
  | 'done' // exited of its own accord, successfully
  | 'crashed' // exited non-zero, so the run is not what it appears

export interface Span {
  kind: SpanKind
  /** Milliseconds from the run's T0. */
  from: number
  /**
   * Milliseconds from T0, or null when the run ended without this span
   * closing. A pending lane has no right edge and must not be drawn with a
   * fabricated one.
   */
  to: number | null
}

export interface Lane {
  name: string
  outcome: Outcome
  spans: Span[]
  dependsOn: WireDependency[]
  exitCode?: number
  crashLog?: string[]
  /**
   * The recoverable milliseconds on this lane, or null when no claim is
   * available. Only quantization counts: the service was ready and its own
   * healthcheck had not yet been run to notice. A service that never passed,
   * or that has no readiness signal at all, has no dead time — it has a
   * different problem.
   */
  deadMs: number | null
  /**
   * Inclusive cost: T0 to the moment this service was ready, so it carries
   * everything it waited on. Null if it never became ready.
   *
   * This is the borrow from a call-graph profiler, where a function's
   * inclusive cost is its own time plus its callees'. Sorting a stack by it
   * answers "who is on the hook for the wall clock", which no per-service
   * duration can — a service that boots in 200ms but waits 40s for a
   * dependency is the one you were looking for.
   */
  readyMs: number | null
  /** Exclusive cost: the service's own time from starting to being ready. */
  ownMs: number | null
}

export interface Run {
  project: string
  startedAt: Date
  /** The right edge of the time axis: the last moment anything was observed. */
  durationMs: number
  lanes: Lane[]
  /** Up before the run began, so unmeasured. Listed, never drawn as a lane. */
  alreadyRunning: string[]
}

export class UnsupportedFormatError extends Error {
  readonly found: number

  constructor(found: number) {
    super(
      `run file is format version ${found}, and this viewer reads version ` +
        `${SUPPORTED_FORMAT_VERSION}`,
    )
    this.name = 'UnsupportedFormatError'
    this.found = found
  }
}

/** Parses a run file into the model the timeline draws. */
export function parseRun(doc: WireDocument): Run {
  if (doc.version !== SUPPORTED_FORMAT_VERSION) {
    throw new UnsupportedFormatError(doc.version)
  }

  const t0 = new Date(doc.startedAt)
  const origin = t0.getTime()
  const offset = (stamp?: string): number | undefined =>
    stamp === undefined ? undefined : new Date(stamp).getTime() - origin

  const lanes = doc.services.map((svc) => toLane(svc, offset))

  // The axis ends at the last observed moment. A pending lane runs to the
  // edge without claiming it ended there.
  const durationMs = Math.max(
    0,
    ...lanes.flatMap((lane) =>
      lane.spans.flatMap((span) => (span.to === null ? [span.from] : [span.to])),
    ),
  )

  return {
    project: doc.project,
    startedAt: t0,
    durationMs,
    lanes,
    alreadyRunning: doc.alreadyRunning ?? [],
  }
}

function toLane(
  svc: WireService,
  offset: (stamp?: string) => number | undefined,
): Lane {
  const created = offset(svc.created)
  const started = offset(svc.started)
  const predicateTrue = offset(svc.predicateTrue)
  const declaredHealthy = offset(svc.declaredHealthy)
  const declaredUnhealthy = offset(svc.declaredUnhealthy)
  const accepting = offset(svc.accepting)
  const exited = offset(svc.exited)

  const spans: Span[] = []

  // The gap from created to started only counts as a wait when something was
  // holding the service. Ungated, it is Docker's own create-to-start latency —
  // real, but not a dependency's fault, and drawing it as one would invent an
  // edge the compose file does not have.
  const gated = (svc.dependsOn ?? []).length > 0
  if (created !== undefined && started !== undefined && started > created) {
    spans.push({ kind: gated ? 'wait' : 'create', from: created, to: started })
  }

  if (started !== undefined) {
    if (predicateTrue !== undefined) {
      spans.push({ kind: 'boot', from: started, to: predicateTrue })

      // The finding. Ready at `predicateTrue`, and nothing acted on it until
      // `declaredHealthy` — a gap only a healthcheck interval explains.
      if (declaredHealthy !== undefined && declaredHealthy > predicateTrue) {
        spans.push({ kind: 'dead', from: predicateTrue, to: declaredHealthy })
      }
    } else if (declaredUnhealthy !== undefined) {
      // Probed and never passed. The span looks like dead time and is not:
      // there was no moment of readiness to be late about.
      spans.push({ kind: 'probing', from: started, to: declaredUnhealthy })
    } else if (accepting !== undefined) {
      spans.push({ kind: 'boot', from: started, to: accepting })
      spans.push({ kind: 'ran', from: accepting, to: exited ?? null })
    } else if (exited !== undefined) {
      // A run-to-completion service and a crashed one both just stop, and the
      // timeline has to tell them apart — a stack whose migrations died looks
      // healthy from every other angle.
      const kind = svc.outcome === 'crashed' ? 'crashed' : 'done'
      spans.push({ kind, from: started, to: exited })
    } else {
      // Nothing ever resolved. Left open on purpose.
      spans.push({ kind: 'ran', from: started, to: null })
    }
  }

  // A healthy service keeps running after it is declared so; a crashed one
  // stops. Either way the lane continues from its last known moment.
  const settled = declaredHealthy ?? declaredUnhealthy
  if (settled !== undefined && exited !== undefined && exited > settled) {
    spans.push({ kind: 'done', from: settled, to: exited })
  }

  const deadMs =
    predicateTrue !== undefined && declaredHealthy !== undefined
      ? Math.max(0, declaredHealthy - predicateTrue)
      : null

  // Ready means whatever this service's readiness signal was: a healthcheck
  // passing, a port accepting, or — for run-to-completion — exiting. A
  // service that never got there has no cost to attribute, not a cost of
  // zero.
  const readyAt = declaredHealthy ?? accepting ?? exited
  const readyMs = readyAt ?? null
  const ownMs =
    readyAt !== undefined && started !== undefined
      ? Math.max(0, readyAt - started)
      : null

  return {
    name: svc.name,
    outcome: svc.outcome,
    spans,
    dependsOn: svc.dependsOn ?? [],
    exitCode: svc.exitCode,
    crashLog: svc.crashLog,
    deadMs,
    readyMs,
    ownMs,
  }
}
