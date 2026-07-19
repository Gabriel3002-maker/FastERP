import { useEffect, useState } from 'react'
import { Plus, Edit2, Trash2, Globe } from 'lucide-react'

interface Page { id: string; title: string; slug: string; published: boolean }

export default function SiteBuilder() {
  const [pages, setPages] = useState<Page[]>([])
  const [loading, setLoading] = useState(true)

  const ah = () => ({
    'Authorization': 'Bearer ' + localStorage.getItem('fasterp_token'),
    'X-Tenant-ID': localStorage.getItem('fasterp_tenant_id') || ''
  })

  useEffect(() => {
    fetch('/api/sitio_web/web_page', { headers: ah() })
      .then(r => r.json())
      .then(data => setPages(Array.isArray(data) ? data : []))
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  return (
    <div className="space-y-6 animate-fadeIn">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>Sitio Web</h1>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>{pages.length} páginas</p>
        </div>
        <button className="premium-btn px-4 py-2.5 text-sm font-semibold flex items-center gap-2">
          <Plus size={18} />
          Nueva página
        </button>
      </div>

      {loading ? (
        <div style={{ color: 'var(--text-tertiary)' }} className="text-center py-10">Cargando...</div>
      ) : pages.length === 0 ? (
        <div className="text-center py-10 rounded-xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <Globe size={32} className="mx-auto mb-3" style={{ color: 'var(--text-tertiary)' }} />
          <p style={{ color: 'var(--text-primary)' }} className="font-semibold">Sin páginas</p>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>Crea la primera página</p>
        </div>
      ) : (
        <div className="rounded-xl overflow-hidden" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <table className="w-full text-sm">
            <thead style={{ background: 'var(--bg-tertiary)' }}>
              <tr>
                <th className="px-4 py-3 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>Título</th>
                <th className="px-4 py-3 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>URL</th>
                <th className="px-4 py-3 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>Estado</th>
                <th className="px-4 py-3 text-right font-semibold" style={{ color: 'var(--text-primary)' }}>Acciones</th>
              </tr>
            </thead>
            <tbody>
              {pages.map((p, i) => (
                <tr key={p.id} style={{ borderBottom: i < pages.length - 1 ? '1px solid var(--border-primary)' : 'none' }}>
                  <td className="px-4 py-3 font-medium" style={{ color: 'var(--text-primary)' }}>{p.title}</td>
                  <td className="px-4 py-3" style={{ color: 'var(--text-tertiary)' }}>{p.slug}</td>
                  <td className="px-4 py-3">
                    <span className="px-2 py-1 rounded text-xs font-bold"
                      style={{ background: p.published ? 'var(--success-bg)' : 'var(--bg-tertiary)', color: p.published ? 'var(--success)' : 'var(--text-tertiary)' }}>
                      {p.published ? 'Publicado' : 'Borrador'}
                    </span>
                  </td>
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
