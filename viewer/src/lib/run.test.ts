// The rules that turn a run file into lanes.
//
// These are checked against fixtures/run.json rather than hand-built objects,
// because the fixture is written by the Go encoder and so cannot drift from
// the format in a way a hand-built object would hide. Every rule here is one
// a drawing routine would plausibly get wrong, and two of them were wrong.

import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'
import {
  parseRun,
  UnsupportedFormatError,
  SUPPORTED_FORMAT_VERSION,
  type Lane,
  type WireDocument,
} from './run'

const doc = JSON.parse(
  readFileSync(new URL('../../fixtures/run.json', import.meta.url), 'utf8'),
) as WireDocument

const run = parseRun(doc)
const lane = (name: string): Lane => {
  const found = run.lanes.find((candidate) => candidate.name === name)
  if (found === undefined) throw new Error(`no ${name} in the fixture`)
  return found
}
const kinds = (name: string) => lane(name).spans.map((span) => span.kind)

describe('dead time', () => {
  it('is claimed where a healthcheck passed late', () => {
    // Ready at 412ms, declared healthy at 5310ms. The whole point of the tool.
    expect(lane('postgres').deadMs).toBeCloseTo(4898)
  })

  it('is not claimed for a service that never passed', () => {
    // metrics probed until 31.2s and failed. That time was never recoverable,
    // and counting it would inflate the finding by more than the finding.
    expect(lane('metrics').outcome).toBe('unhealthy')
    expect(lane('metrics').deadMs).toBeNull()
  })

  it('is not claimed for a service with no readiness signal', () => {
    expect(lane('docs').outcome).toBe('no-readiness')
    expect(lane('docs').deadMs).toBeNull()
  })

  it('totals only the recoverable lanes', () => {
    const total = run.lanes.reduce((sum, each) => sum + (each.deadMs ?? 0), 0)
    expect(total).toBeLessThan(lane('metrics').spans[1].to!)
  })
})

describe('waiting', () => {
  it('is a wait only when something was holding the service', () => {
    expect(lane('api').dependsOn.length).toBeGreaterThan(0)
    expect(kinds('api')).toContain('wait')
  })

  it('is container create latency when nothing gates it', () => {
    // postgres has no depends_on, so its created-to-started gap is Docker's
    // own latency. Drawing it as a wait would invent an edge.
    expect(lane('postgres').dependsOn).toHaveLength(0)
    expect(kinds('postgres')).toContain('create')
    expect(kinds('postgres')).not.toContain('wait')
  })
})

describe('spans', () => {
  it('leaves a lane open when nothing ever resolved it', () => {
    const open = lane('reports').spans.at(-1)!
    expect(lane('reports').outcome).toBe('pending')
    expect(open.to).toBeNull()
  })

  it('tells a crash apart from a clean exit', () => {
    expect(kinds('search')).toContain('crashed')
    expect(kinds('migrations')).toContain('done')
    expect(kinds('migrations')).not.toContain('crashed')
  })

  it('marks an unhealthy service probing, not dead', () => {
    expect(kinds('metrics')).toContain('probing')
    expect(kinds('metrics')).not.toContain('dead')
  })
})

describe('cost', () => {
  it('separates what a service waited on from what it did', () => {
    const api = lane('api')
    // Ready at 9.99s having done 2.84s of its own work: the rest was its
    // dependencies, which is the ordering the timeline alone cannot give.
    expect(api.readyMs).toBeCloseTo(9990)
    expect(api.ownMs).toBeCloseTo(2840)
    expect(api.ownMs!).toBeLessThan(api.readyMs!)
  })

  it('reports no cost, rather than zero, for a service that never readied', () => {
    expect(lane('reports').readyMs).toBeNull()
    expect(lane('reports').ownMs).toBeNull()
  })
})

describe('the document', () => {
  it('ends the axis at the last observed moment', () => {
    expect(run.durationMs).toBeCloseTo(31_200)
  })

  it('keeps unmeasured services out of the lanes', () => {
    expect(run.alreadyRunning).toContain('mailhog')
    expect(run.lanes.map((each) => each.name)).not.toContain('mailhog')
  })

  it('refuses a format it does not read', () => {
    expect(() =>
      parseRun({ ...doc, version: SUPPORTED_FORMAT_VERSION + 1 }),
    ).toThrow(UnsupportedFormatError)
  })
})
