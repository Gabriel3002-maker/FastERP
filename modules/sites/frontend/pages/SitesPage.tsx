import { useEffect, useState } from 'react'
import { Plus, Edit2, Trash2, MapPin } from 'lucide-react'

interface Site { id: string; client_name: string; project_address: string; status: string }

export default function SitesPage() {
  const [sites, setSites] = useState<Site[]>([])
  const [loading, setLoading] = useState(true)

  const ah = () => ({
    'Authorization': 'Bearer ' + localStorage.getItem('fasterp_token'),
    'X-Tenant-ID': localStorage.getItem('fasterp_tenant_id') || ''
  })

  useEffect(() => {
    fetch('/api/sites/site', { headers: ah() })
      .then(r => r.json())
      .then(data => setSites(Array.isArray(data) ? data : []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return (
    <div className="space-y-6 animate-fadeIn">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>Sitios Solares</h1>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>{sites.length} sitios</p>
        </div>
        <button className="premium-btn px-4 py-2.5 text-sm font-semibold flex items-center gap-2">
          <Plus size={18} />
          Nuevo sitio
        </button>
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-tertiary)' }} className="text-center py-10">Cargando...</div>
      ) : sites.length === 0 ? (
        <div className="text-center py-10 rounded-xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <MapPin size={32} className="mx-auto mb-3" style={{ color: 'var(--text-tertiary)' }} />
          <p style={{ color: 'var(--text-primary)' }} className="font-semibold">Sin sitios</p>
        </div>
      ) : (
        <div className="rounded-xl overflow-hidden" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <table className="w-full text-sm">
            <thead style={{ background: 'var(--bg-tertiary)' }}>
              <tr>
                <th className="px-4 py-3 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>Cliente</th>
                <th className="px-4 py-3 text-left font-semibold hidden sm:table-cell" style={{ color: 'var(--text-primary)' }}>Ubicación</th>
                <th className="px-4 py-3 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>Estado</th>
                <th className="px-4 py-3 text-right font-semibold" style={{ color: 'var(--text-primary)' }}>Acciones</th>
              </tr>
            </thead>
            <tbody>
              {sites.map((s, i) => (
                <tr key={s.id} style={{ borderBottom: i < sites.length - 1 ? '1px solid var(--border-primary)' : 'none' }}>
                  <td className="px-4 py-3 font-medium" style={{ color: 'var(--text-primary)' }}>{s.client_name}</td>
                  <td className="px-4 py-3 hidden sm:table-cell" style={{ color: 'var(--text-tertiary)' }}>{s.project_address}</td>
                  <td className="px-4 py-3">{s.status}</td>
                  <td className="px-4 py-3 text-right flex gap-1 justify-end">
                    <button className="p-1.5 rounded-lg" style={{ color: '#3b82f6' }}><Edit2 size={16} /></button>
                    <button className="p-1.5 rounded-lg" style={{ color: '#dc2626' }}><Trash2 size={16} /></button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
