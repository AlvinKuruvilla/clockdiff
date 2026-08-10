// The cost pane.
//
// A timeline shows structure; it does not rank. This ranks — sorted by
// inclusive cost, so the service on the hook for the wall clock is the first
// row whether or not it did any of the work itself. Selection is shared with
// the timeline, so a row and its lane are the same object seen twice.

import { deadTimeReason, type Lane, type Run } from '@/lib/run'
import { formatDuration } from '@/lib/scale'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

export type CostColumn = 'readyMs' | 'ownMs' | 'deadMs'

const COLUMNS: { key: CostColumn; label: string; help: string }[] = [
  {
    key: 'readyMs',
    label: 'ready at',
    help: 'from the start of the run, including everything it waited on',
  },
  { key: 'ownMs', label: 'own', help: 'its own time from starting to ready' },
  {
    key: 'deadMs',
    label: 'dead',
    help: 'ready this long before anything noticed — recoverable',
  },
]

export interface CostTableProps {
  run: Run
  sortBy: CostColumn
  onSortBy: (column: CostColumn) => void
  selected: string | null
  onSelect: (name: string | null) => void
}

export function CostTable({
  run,
  sortBy,
  onSortBy,
  selected,
  onSelect,
}: CostTableProps) {
  // Descending, and services with no measurement for the sort column go last
  // rather than sorting as zero — "never became ready" is not "became ready
  // instantly".
  const rows = [...run.lanes].sort((a, b) => {
    const left = a[sortBy]
    const right = b[sortBy]
    if (left === null && right === null) return a.name.localeCompare(b.name)
    if (left === null) return 1
    if (right === null) return -1
    return right - left
  })

  return (
    <div className="h-full overflow-auto">
      <Table className="text-[11px]">
        <TableHeader className="sticky top-0 z-10 bg-background">
          <TableRow>
            <TableHead className="h-7 font-mono text-[10px] tracking-wide uppercase">
              service
            </TableHead>
            {COLUMNS.map((column) => (
              <TableHead
                key={column.key}
                className="h-7 w-24 text-right font-mono text-[10px] tracking-wide uppercase"
              >
                <button
                  className={`hover:text-foreground ${
                    sortBy === column.key ? 'text-foreground' : ''
                  }`}
                  onClick={() => onSortBy(column.key)}
                  title={column.help}
                  type="button"
                >
                  {column.label}
                  {sortBy === column.key ? ' ↓' : ''}
                </button>
              </TableHead>
            ))}
            <TableHead className="h-7 font-mono text-[10px] tracking-wide uppercase">
              outcome
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((lane) => (
            <CostRow
              key={lane.name}
              lane={lane}
              selected={lane.name === selected}
              onSelect={onSelect}
            />
          ))}
        </TableBody>
      </Table>
    </div>
  )
}

function CostRow({
  lane,
  selected,
  onSelect,
}: {
  lane: Lane
  selected: boolean
  onSelect: (name: string | null) => void
}) {
  return (
    <TableRow
      className={`cursor-pointer ${selected ? 'bg-accent' : ''}`}
      onClick={() => onSelect(selected ? null : lane.name)}
    >
      <TableCell className="py-1 font-mono">{lane.name}</TableCell>
      <TableCell className="py-1 text-right font-mono tabular-nums">
        {lane.readyMs === null ? '—' : formatDuration(lane.readyMs)}
      </TableCell>
      <TableCell className="py-1 text-right font-mono tabular-nums text-muted-foreground">
        {lane.ownMs === null ? '—' : formatDuration(lane.ownMs)}
      </TableCell>
      <TableCell
        className="py-1 text-right font-mono tabular-nums"
        style={
          lane.deadMs !== null && lane.deadMs > 0
            ? { color: 'var(--span-dead)' }
            : undefined
        }
        title={deadTimeReason(lane) ?? undefined}
      >
        {lane.deadMs === null ? '—' : formatDuration(lane.deadMs)}
      </TableCell>
      <TableCell className="py-1 font-mono text-muted-foreground">
        {lane.outcome}
      </TableCell>
    </TableRow>
  )
}
