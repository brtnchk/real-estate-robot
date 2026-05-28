import { useState } from 'react'
import { CITIES, CATEGORIES, type CityOption } from './cities'

interface Props {
  onCrawlStarted: (cityName: string) => void
}

export function SearchPanel({ onCrawlStarted }: Props) {
  const [selectedCity, setSelectedCity] = useState<CityOption | null>(null)
  const [categorySlug, setCategorySlug] = useState(CATEGORIES[0].slug)
  const [status, setStatus] = useState<'idle' | 'loading' | 'error'>('idle')
  const [errorMsg, setErrorMsg] = useState('')

  async function handleSearch() {
    if (!selectedCity) return
    setStatus('loading')
    setErrorMsg('')
    try {
      const res = await fetch('/api/crawl', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          city_slug: selectedCity.slug,
          category_slug: categorySlug,
        }),
      })
      if (!res.ok) {
        const err = await res.json().catch(() => ({}))
        throw new Error((err as { error?: string }).error ?? `HTTP ${res.status}`)
      }
      onCrawlStarted(selectedCity.name)
      setStatus('idle')
    } catch (e) {
      setErrorMsg(String(e))
      setStatus('error')
    }
  }

  // Group cities by region for the optgroup UI.
  const regions = Array.from(new Set(CITIES.map((c) => c.region ?? '')))

  return (
    <div className="search-panel">
      <h2 className="search-panel-title">Шукати місто</h2>

      <div className="search-row">
        <label className="search-label">
          <span>Місто</span>
          <select
            value={selectedCity?.slug ?? ''}
            onChange={(e) => {
              const found = CITIES.find((c) => c.slug === e.target.value) ?? null
              setSelectedCity(found)
            }}
          >
            <option value="">— оберіть місто —</option>
            {regions.map((region) => (
              <optgroup key={region} label={region}>
                {CITIES.filter((c) => (c.region ?? '') === region).map((c) => (
                  <option key={c.slug} value={c.slug}>
                    {c.name}
                  </option>
                ))}
              </optgroup>
            ))}
          </select>
        </label>

        <label className="search-label">
          <span>Категорія</span>
          <select value={categorySlug} onChange={(e) => setCategorySlug(e.target.value)}>
            {CATEGORIES.map((c) => (
              <option key={c.slug} value={c.slug}>
                {c.name}
              </option>
            ))}
          </select>
        </label>

        <button
          className="search-btn"
          disabled={!selectedCity || status === 'loading'}
          onClick={handleSearch}
        >
          {status === 'loading' ? 'Запускаємо…' : 'Шукати'}
        </button>
      </div>

      {status === 'error' && (
        <p className="search-error">
          ⚠ {errorMsg}. Переконайтеся, що запущені воркери (make discovery, make fetcher, make parser).
        </p>
      )}
    </div>
  )
}