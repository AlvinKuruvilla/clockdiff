// The shell.
//
// Fixed to the viewport with resizable panes rather than a scrolling
// document: a profiler is read by moving between a timeline and a ranking of
// the same run, and losing one to scroll while reading the other breaks that.

import { useEffect, useState } from 'react'
import { CostTable, type CostColumn } from '@/components/CostTable'
import { Timeline } from '@/components/Timeline'
import {
  ResizableHandle,
  ResizablePanel,
  ResizablePanelGroup,
} from '@/components/ui/resizable'
import {
  parseRun,
  UnsupportedFormatError,
  type Lane,
  type Run,
  type WireDocument,
} from '@/lib/run'
import { formatDuration } from '@/lib/scale'

// Where the run comes from. The Go server will serve it at this path; in dev,
// Vite serves the fixture from `public/`. Either way the viewer only ever
// knows about a run file, never about Docker.
const RUN_URL = './run.json'

export default function App() {
  const [run, setRun] = useState<Run | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)
  const [sortBy, setSortBy] = useState<CostColumn>('readyMs')

  useEffect(() => {
    fetch(RUN_URL)
      .then((response) => {
        if (!response.ok) {
          throw new Error(`${RUN_URL}: ${response.status} ${response.statusText}`)
        }
        return response.json() as Promise<WireDocument>
      })
      .then((doc) => setRun(parseRun(doc)))
      .catch((cause: unknown) => {
        setError(
          cause instanceof UnsupportedFormatError || cause instanceof Error
            ? cause.message
            : String(cause),
        )
      })
  }, [])

  if (error !== null) {
    return (
      <main className="p-6 font-mono text-xs text-destructive">
        <p>could not load the run</p>
        <p className="mt-1 opacity-70">{error}</p>
      </main>
    )
  }

  if (run === null) {
    return (
      <main className="p-6 font-mono text-xs text-muted-foreground">
        loading…
      </main>
    )
  }

  const recoverable = run.lanes.reduce(
    (total, lane) => total + (lane.deadMs ?? 0),
    0,
  )
  const selectedLane = run.lanes.find((lane) => lane.name === selected) ?? null

  return (
    <div className="flex h-screen flex-col overflow-hidden bg-background text-foreground">
      <header className="flex shrink-0 items-center gap-4 border-b px-3 py-1.5">
        <span className="font-mono text-xs font-medium">{run.project}</span>
        <span className="font-mono text-[11px] text-muted-foreground">
          {run.startedAt.toLocaleString()}
        </span>
        <Stat label="settled" value={formatDuration(run.durationMs)} />
        <Stat
          label="recoverable"
          value={formatDuration(recoverable)}
          accent={recoverable > 0}
        />
        <Stat label="services" value={String(run.lanes.length)} />
        {run.alreadyRunning.length > 0 && (
          <span
            className="ml-auto font-mono text-[11px] text-muted-foreground"
            title="up before the run began, so nothing about them was measured"
          >
            unmeasured: {run.alreadyRunning.join(', ')}
          </span>
        )}
      </header>

      {/* Sizes are strings on purpose: this library reads a number as pixels
          and a bare string as a percentage. */}
      <ResizablePanelGroup className="min-h-0 flex-1" orientation="vertical">
        <ResizablePanel defaultSize="58" minSize="25">
          <Timeline run={run} selected={selected} onSelect={setSelected} />
        </ResizablePanel>
        <ResizableHandle withHandle />
        <ResizablePanel defaultSize="42" minSize="15">
          <ResizablePanelGroup orientation="horizontal">
            <ResizablePanel defaultSize="62" minSize="30">
              <CostTable
                run={run}
                sortBy={sortBy}
                onSortBy={setSortBy}
                selected={selected}
                onSelect={setSelected}
              />
            </ResizablePanel>
            <ResizableHandle withHandle />
            <ResizablePanel defaultSize="38" minSize="20">
              <Inspector lane={selectedLane} />
            </ResizablePanel>
          </ResizablePanelGroup>
        </ResizablePanel>
      </ResizablePanelGroup>
    </div>
  )
}

function Stat({
  label,
  value,
  accent = false,
}: {
  label: string
  value: string
  accent?: boolean
}) {
  return (
    <span className="flex items-baseline gap-1 font-mono text-[11px]">
      <span className="text-muted-foreground">{label}</span>
      <span
        className="tabular-nums"
        style={accent ? { color: 'var(--span-dead)' } : undefined}
      >
        {value}
      </span>
    </span>
  )
}

function Inspector({ lane }: { lane: Lane | null }) {
  if (lane === null) {
    return (
      <div className="flex h-full items-center justify-center p-3 font-mono text-[11px] text-muted-foreground">
        select a service
      </div>
    )
  }

  return (
    <div className="h-full overflow-auto border-l p-3 font-mono text-[11px]">
      <h2 className="font-medium">{lane.name}</h2>
      <dl className="mt-2 grid grid-cols-[7rem_1fr] gap-y-1">
        <Field label="outcome">{lane.outcome}</Field>
        <Field label="ready at">
          {lane.readyMs === null ? 'never' : formatDuration(lane.readyMs)}
        </Field>
        <Field label="own time">
          {lane.ownMs === null ? '—' : formatDuration(lane.ownMs)}
        </Field>
        <Field label="dead time">
          {lane.deadMs === null
            ? 'no claim available'
            : formatDuration(lane.deadMs)}
        </Field>
        {lane.exitCode !== undefined && (
          <Field label="exit code">{lane.exitCode}</Field>
        )}
        {lane.dependsOn.length > 0 && (
          <Field label="waits on">
            <ul>
              {lane.dependsOn.map((dep) => (
                <li key={dep.service}>
                  {dep.service}{' '}
                  <span className="text-muted-foreground">{dep.condition}</span>
                </li>
              ))}
            </ul>
          </Field>
        )}
      </dl>
      {lane.crashLog !== undefined && (
        <pre className="mt-3 overflow-x-auto rounded border bg-muted/40 p-2 text-[10px] whitespace-pre-wrap">
          {lane.crashLog.join('\n')}
        </pre>
      )}
    </div>
  )
}

function Field({
  label,
  children,
}: {
  label: string
  children: React.ReactNode
}) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd>{children}</dd>
    </>
  )
}
