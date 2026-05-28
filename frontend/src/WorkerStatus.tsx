import { useEffect, useState } from 'react'

interface WorkerInfo {
  name: string
  queue: string
  running: boolean
  consumers: number
}

const LABELS: Record<string, string> = {
  discovery: 'Discovery',
  fetcher:   'Fetcher',
  parser:    'Parser',
  enricher:  'Enricher',
}

export function WorkerStatus() {
  const [workers, setWorkers] = useState<WorkerInfo[]>([])

  function refresh() {
    fetch('/api/worker-status')
      .then((r) => r.json())
      .then((d: { workers: WorkerInfo[] }) => setWorkers(d.workers))
      .catch(() => {})
  }

  useEffect(() => {
    refresh()
    const t = setInterval(refresh, 10_000) // re-check every 10 s
    return () => clearInterval(t)
  }, [])

  if (workers.length === 0) return null

  return (
    <div className="worker-status">
      <span className="worker-status-label">Воркери:</span>
      {workers.map((w) => (
        <span
          key={w.name}
          className={`worker-chip ${w.running ? 'worker-chip--ok' : 'worker-chip--off'}`}
          title={`queue: ${w.queue} · consumers: ${w.consumers}`}
        >
          <span className="worker-dot" />
          {LABELS[w.name] ?? w.name}
        </span>
      ))}
    </div>
  )
}