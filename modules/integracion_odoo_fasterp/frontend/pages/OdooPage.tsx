import { useEffect, useState } from 'react'
import { RefreshCw, Settings, CheckCircle } from 'lucide-react'

export default function OdooPage() {
  const [loading, setLoading] = useState(true)
  const [syncing, setSyncing] = useState(false)
  const [status, setStatus] = useState<any>(null)

  const ah = () => ({
    'Authorization': 'Bearer ' + localStorage.getItem('fasterp_token'),
    'X-Tenant-ID': localStorage.getItem('fasterp_tenant_id') || ''
  })

  useEffect(() => {
    setLoading(false)
  }, [])

  const handleSync = async () => {
    setSyncing(true)
    try {
      const res = await fetch('/api/odoo/sync/products', {
        method: 'POST',
        headers: { ...ah(), 'Content-Type': 'application/json' },
        body: JSON.stringify({ limit: 0 })
      })
      const data = await res.json()
      setStatus(data)
    } catch (e) {
      console.error(e)
    } finally {
      setSyncing(false)
    }
  }

  return (
    <div className="space-y-6 animate-fadeIn">
      <div className="flex justify-between items-center">
        <div>
          <h1 className="text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>Integración Odoo</h1>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>Sincronización bidireccional con Odoo</p>
        </div>
        <button
          onClick={handleSync}
          disabled={syncing}
          className="premium-btn px-4 py-2.5 text-sm font-semibold flex items-center gap-2">
          <RefreshCw size={18} className={syncing ? 'animate-spin' : ''} />
          {syncing ? 'Sincronizando...' : 'Sincronizar'}
        </button>
      </div>

      {status && (
        <div className="rounded-xl p-4 sm:p-6" style={{ background: 'var(--success-bg)', border: '1px solid var(--success)' }}>
          <div className="flex items-start gap-3">
            <CheckCircle size={20} style={{ color: 'var(--success)', marginTop: '2px' }} className="flex-shrink-0" />
            <div>
              <p className="font-semibold" style={{ color: 'var(--success)' }}>Sincronización completada</p>
              <p className="text-sm mt-1" style={{ color: 'var(--success)' }}>
                {status.records_count} registro{status.records_count !== 1 ? 's' : ''} sincronizado{status.records_count !== 1 ? 's' : ''}
              </p>
            </div>
          </div>
        </div>
      )}

      <div className="grid sm:grid-cols-2 gap-6">
        {/* Productos */}
        <div className="rounded-xl p-6" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <h3 className="font-bold text-lg mb-3" style={{ color: 'var(--text-primary)' }}>Productos</h3>
          <p className="text-sm" style={{ color: 'var(--text-tertiary)' }}>Sincronizar productos desde Odoo</p>
          <button className="mt-4 px-4 py-2 text-sm rounded-lg" style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}>
            Sincronizar ahora
          </button>
        </div>

        {/* Pedidos */}
        <div className="rounded-xl p-6" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <h3 className="font-bold text-lg mb-3" style={{ color: 'var(--text-primary)' }}>Pedidos</h3>
          <p className="text-sm" style={{ color: 'var(--text-tertiary)' }}>Sincronizar órdenes de venta</p>
          <button className="mt-4 px-4 py-2 text-sm rounded-lg" style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}>
            Sincronizar ahora
          </button>
        </div>

        {/* Contactos */}
        <div className="rounded-xl p-6" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <h3 className="font-bold text-lg mb-3" style={{ color: 'var(--text-primary)' }}>Contactos</h3>
          <p className="text-sm" style={{ color: 'var(--text-tertiary)' }}>Sincronizar clientes y leads</p>
          <button className="mt-4 px-4 py-2 text-sm rounded-lg" style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}>
            Sincronizar ahora
          </button>
        </div>

        {/* Configuración */}
        <div className="rounded-xl p-6" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <h3 className="font-bold text-lg mb-3" style={{ color: 'var(--text-primary)' }}>Configuración</h3>
          <p className="text-sm" style={{ color: 'var(--text-tertiary)' }}>Conexión y credenciales</p>
          <button className="mt-4 px-4 py-2 text-sm rounded-lg flex items-center gap-2" style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}>
            <Settings size={16} />
            Configurar
          </button>
        </div>
      </div>
    </div>
  )
}
