import { useEffect, useState } from 'react'
import { WorkerStatus } from './WorkerStatus'

interface Listing {
  listing_id: number
  olx_user_id: string
  url: string
  title: string
  price?: number
  currency?: string
  city?: string
  district?: string
  property_type?: string
  deal_type?: string
  posted_at?: string
  seller_name?: string
  is_business: boolean
  manual_label?: string   // 'owner' | 'agency' | undefined
  seller_listings: number
  seller_districts: number
  real_seller_score: number
}

interface Stats {
  private_sellers: number
  business_sellers: number
  private_avg_score: number
  business_avg_score: number
  last_parsed_at?: string
}

interface Category {
  property_type: string
  deal_type?: string
  count: number
}

interface CityCount {
  city: string
  count: number
}

const DEAL_TYPE_LABELS: Record<string, string> = {
  sale: 'продаж',
  rent_long: 'довгострокова оренда',
  rent_short: 'подобово',
}

const AGE_OPTIONS = [
  { value: 1, label: 'last 24 hours' },
  { value: 7, label: 'last 7 days' },
  { value: 30, label: 'last 30 days' },
  { value: 90, label: 'last 90 days' },
  { value: 365, label: 'last year' },
  { value: 99999, label: 'all' },
]


function formatRelative(iso: string): string {
  const diff = Math.floor((Date.now() - new Date(iso).getTime()) / 1000)
  if (diff < 60) return `${diff} сек тому`
  if (diff < 3600) return `${Math.floor(diff / 60)} хв тому`
  if (diff < 86400) return `${Math.floor(diff / 3600)} год тому`
  return new Date(iso).toLocaleDateString('uk-UA')
}

function scoreClass(s: number): string {
  if (s >= 0.8) return 'score-high'
  if (s >= 0.5) return 'score-mid'
  if (s >= 0.3) return 'score-low'
  return 'score-vlow'
}

function App() {
  const [listings, setListings] = useState<Listing[]>([])
  const [stats, setStats] = useState<Stats | null>(null)
  const [categories, setCategories] = useState<Category[]>([])
  const [cities, setCities] = useState<CityCount[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const [maxAgeDays, setMaxAgeDays] = useState(30)
  const [minScore, setMinScore] = useState(0)
  const [limit, setLimit] = useState(100)
  const [propertyType, setPropertyType] = useState('')
  const [dealType, setDealType] = useState('')
  const [city, setCity] = useState('')


  const refreshDimensions = () => {
    fetch('/api/stats').then((r) => r.json()).then(setStats).catch(() => {})
    fetch('/api/categories').then((r) => r.json()).then(setCategories).catch(() => {})
    fetch('/api/cities').then((r) => r.json()).then(setCities).catch(() => {})
  }

  // Initial load.
  useEffect(() => { refreshDimensions() }, [])

  // Auto-refresh every 60 s — background workers add new data continuously.
  useEffect(() => {
    const t = setInterval(refreshDimensions, 60_000)
    return () => clearInterval(t)
  }, [])


  async function labelSeller(olxUserId: string, label: string | null) {
    await fetch(`/api/sellers/${encodeURIComponent(olxUserId)}/label`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ label }),
    })
    // Optimistic update
    setListings((prev) =>
      prev.map((l) =>
        l.olx_user_id === olxUserId
          ? { ...l, manual_label: label ?? undefined, real_seller_score: label === 'owner' ? 1 : label === 'agency' ? 0 : l.real_seller_score }
          : l
      )
    )
  }

  // Listings re-fetch whenever any filter changes.
  useEffect(() => {
    setLoading(true)
    setError(null)
    const params = new URLSearchParams({
      max_age_days: String(maxAgeDays),
      min_score: String(minScore),
      limit: String(limit),
    })
    if (propertyType) params.set('property_type', propertyType)
    if (dealType) params.set('deal_type', dealType)
    if (city) params.set('city', city)
    fetch(`/api/listings?${params}`)
      .then((r) => {
        if (!r.ok) throw new Error(`HTTP ${r.status}`)
        return r.json()
      })
      .then((data: Listing[]) => {
        setListings(data)
        setLoading(false)
      })
      .catch((e) => {
        setError(String(e))
        setLoading(false)
      })
  }, [maxAgeDays, minScore, limit, propertyType, dealType, city])

  // Distinct property_types in current dataset (with total counts).
  const propertyTypes = Array.from(
    categories.reduce((m, c) => {
      m.set(c.property_type, (m.get(c.property_type) || 0) + c.count)
      return m
    }, new Map<string, number>())
  ).sort((a, b) => b[1] - a[1])

  // Distinct deal_types — narrowed to the selected property type if any.
  const dealTypes = Array.from(
    categories
      .filter((c) => !propertyType || c.property_type === propertyType)
      .reduce((m, c) => {
        if (!c.deal_type) return m
        m.set(c.deal_type, (m.get(c.deal_type) || 0) + c.count)
        return m
      }, new Map<string, number>())
  ).sort((a, b) => b[1] - a[1])

  return (
    <div className="container">
      <WorkerStatus />

      <header>
        <h1>OLX Real Private Sellers</h1>
        {stats && (
          <div className="stats">
            <span>
              <strong>{stats.private_sellers}</strong> приватних
              <em> (avg {stats.private_avg_score.toFixed(2)})</em>
            </span>
            <span>
              <strong>{stats.business_sellers}</strong> бізнес
              <em> (avg {stats.business_avg_score.toFixed(2)})</em>
            </span>
            {stats.last_parsed_at && (
              <span className="last-updated">
                Оновлено: {formatRelative(stats.last_parsed_at)}
              </span>
            )}
          </div>
        )}
      </header>

      <div className="filters">
        <label>
          <span>Місто</span>
          <select value={city} onChange={(e) => setCity(e.target.value)}>
            <option value="">всі міста</option>
            {cities.map((c) => (
              <option key={c.city} value={c.city}>{c.city} ({c.count})</option>
            ))}
          </select>
        </label>

        <label>
          <span>Property type</span>
          <select
            value={propertyType}
            onChange={(e) => {
              setPropertyType(e.target.value)
              setDealType('') // reset deal type when property type changes
            }}
          >
            <option value="">all types</option>
            {propertyTypes.map(([type, n]) => (
              <option key={type} value={type}>
                {type} ({n})
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>Deal type</span>
          <select value={dealType} onChange={(e) => setDealType(e.target.value)}>
            <option value="">all deals</option>
            {dealTypes.map(([type, n]) => (
              <option key={type} value={type}>
                {DEAL_TYPE_LABELS[type] ?? type} ({n})
              </option>
            ))}
          </select>
        </label>

        <label>
          <span>Posted within</span>
          <select value={maxAgeDays} onChange={(e) => setMaxAgeDays(Number(e.target.value))}>
            {AGE_OPTIONS.map((o) => (
              <option key={o.value} value={o.value}>
                {o.label}
              </option>
            ))}
          </select>
        </label>

        <label className="slider-label">
          <span>
            Min score: <strong>{minScore.toFixed(2)}</strong>
          </span>
          <input
            type="range"
            min="0"
            max="1"
            step="0.05"
            value={minScore}
            onChange={(e) => setMinScore(Number(e.target.value))}
          />
        </label>

        <label>
          <span>Limit</span>
          <input
            type="number"
            min={10}
            max={500}
            step={10}
            value={limit}
            onChange={(e) => setLimit(Number(e.target.value))}
          />
        </label>

        <div className="meta">
          {loading ? 'Loading…' : error ? <span className="err">{error}</span> : `${listings.length} listings`}
        </div>
      </div>

      <table>
        <thead>
          <tr>
            <th>Score</th>
            <th>Title</th>
            <th>Price</th>
            <th>District</th>
            <th>Seller</th>
            <th>Posted</th>
          </tr>
        </thead>
        <tbody>
          {listings.map((l) => (
            <tr key={l.listing_id} className={scoreClass(l.real_seller_score)}>
              <td className="score">{l.real_seller_score.toFixed(2)}</td>
              <td>
                <a href={l.url} target="_blank" rel="noopener noreferrer">
                  {l.title}
                </a>
              </td>
              <td className="price">
                {l.price ? `${Math.round(l.price).toLocaleString()} ${l.currency}` : '—'}
              </td>
              <td>{l.district || '—'}</td>
              <td>
                <div className="seller-row">
                  <span>{l.seller_name || '—'}</span>
                  {l.is_business && <span className="biz-badge">BIZ</span>}
                  <span className="label-btns">
                    <button
                      className={`label-btn ${l.manual_label === 'owner' ? 'label-btn--owner' : ''}`}
                      title={l.manual_label === 'owner' ? 'Знято мітку власника' : 'Позначити як власник'}
                      onClick={() => labelSeller(l.olx_user_id, l.manual_label === 'owner' ? null : 'owner')}
                    >👤</button>
                    <button
                      className={`label-btn ${l.manual_label === 'agency' ? 'label-btn--agency' : ''}`}
                      title={l.manual_label === 'agency' ? 'Знято мітку агентства' : 'Позначити як агентство'}
                      onClick={() => labelSeller(l.olx_user_id, l.manual_label === 'agency' ? null : 'agency')}
                    >🏢</button>
                  </span>
                </div>
                <span className="seller-meta">
                  {l.seller_listings} ad{l.seller_listings === 1 ? '' : 's'} · {l.seller_districts}{' '}
                  district{l.seller_districts === 1 ? '' : 's'}
                </span>
              </td>
              <td className="posted">
                {l.posted_at ? new Date(l.posted_at).toLocaleDateString('uk-UA') : '—'}
              </td>
            </tr>
          ))}
        </tbody>
      </table>

      {!loading && listings.length === 0 && (
        <div className="empty">
          No listings match these filters. Try lowering <strong>Min score</strong> or widening{' '}
          <strong>Posted within</strong>.
        </div>
      )}
    </div>
  )
}

export default App