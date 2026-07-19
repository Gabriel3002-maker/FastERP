import { useEffect, useState } from 'react'
import { Package, Edit2, Trash2, Plus, Search } from 'lucide-react'

interface Product {
  odoo_product_id: number
  name: string
  price: number
  stock: number
  published: boolean
  featured: boolean
  image: string
}

export default function StorePage() {
  const [products, setProducts] = useState<Product[]>([])
  const [loading, setLoading] = useState(true)
  const [search, setSearch] = useState('')

  const ah = () => ({
    'Content-Type': 'application/json',
    'Authorization': 'Bearer ' + localStorage.getItem('fasterp_token'),
    'X-Tenant-ID': localStorage.getItem('fasterp_tenant_id') || ''
  })

  const fetchProducts = async () => {
    try {
      const res = await fetch('/api/store/products', { headers: ah() })
      const data = await res.json()
      setProducts(Array.isArray(data) ? data : [])
    } catch (e) {
      console.error(e)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    fetchProducts()
  }, [])

  const filtered = products.filter((p) =>
    !search || p.name.toLowerCase().includes(search.toLowerCase())
  )

  return (
    <div className="space-y-6 animate-fadeIn">
      {/* Header */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>
            Tienda Web
          </h1>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>
            {products.length} producto{products.length !== 1 ? 's' : ''}
          </p>
        </div>
        <button className="premium-btn px-4 py-2.5 text-sm font-semibold flex items-center gap-2">
          <Plus size={18} />
          Nuevo producto
        </button>
      </div>

      {/* Search */}
      <div className="relative">
        <Search size={18} className="absolute left-3 top-1/2 -translate-y-1/2" style={{ color: 'var(--text-tertiary)' }} />
        <input
          placeholder="Buscar productos..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full pl-10 pr-4 py-2.5 rounded-lg text-sm transition-all focus:outline-none focus:ring-2"
          style={{
            background: 'var(--bg-input)',
            border: '1px solid var(--border-primary)',
            color: 'var(--text-primary)',
            '--tw-ring-color': '#3b82f6'
          } as any}
        />
      </div>

      {/* Grid */}
      {loading ? (
        <div className="text-center py-10" style={{ color: 'var(--text-tertiary)' }}>
          Cargando productos...
        </div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-10 rounded-xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <Package size={32} className="mx-auto mb-3" style={{ color: 'var(--text-tertiary)' }} />
          <p style={{ color: 'var(--text-primary)' }} className="font-semibold">
            Sin productos
          </p>
          <p className="text-sm mt-1" style={{ color: 'var(--text-tertiary)' }}>
            Sincroniza productos desde Odoo
          </p>
        </div>
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {filtered.map((p) => (
            <div key={p.odoo_product_id}
              className="rounded-xl overflow-hidden group"
              style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
              <div className="aspect-square bg-gray-200 overflow-hidden">
                {p.image ? (
                  <img src={p.image} alt={p.name} className="w-full h-full object-cover group-hover:scale-105 transition-transform" />
                ) : (
                  <div className="w-full h-full flex items-center justify-center" style={{ background: 'var(--bg-tertiary)' }}>
                    <Package size={32} style={{ color: 'var(--text-tertiary)' }} />
                  </div>
                )}
              </div>
              <div className="p-4">
                <h3 className="font-semibold line-clamp-2" style={{ color: 'var(--text-primary)' }}>
                  {p.name}
                </h3>
                <div className="mt-3 flex items-end justify-between">
                  <div>
                    <p className="text-2xl font-bold" style={{ color: '#3b82f6' }}>
                      ${p.price}
                    </p>
                    <p className="text-xs mt-1" style={{ color: 'var(--text-tertiary)' }}>
                      {p.stock} en stock
                    </p>
                  </div>
                  <div className="flex gap-1">
                    <button className="p-2 rounded-lg hover:bg-blue-100" style={{ background: 'var(--bg-input)' }}>
                      <Edit2 size={16} style={{ color: '#3b82f6' }} />
                    </button>
                    <button className="p-2 rounded-lg hover:bg-red-100" style={{ background: 'var(--bg-input)' }}>
                      <Trash2 size={16} style={{ color: '#dc2626' }} />
                    </button>
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}
    </div>
  )
}
