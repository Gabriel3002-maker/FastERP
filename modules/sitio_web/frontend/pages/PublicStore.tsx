import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ImageIcon, Star, Search, Package, ShoppingCart, ArrowRight, Zap, Shield, Truck } from 'lucide-react'
import { storeListProducts, type StoreProductCard } from '../../../../src/api/client'

export default function PublicStore() {
  const navigate = useNavigate()
  const [items, setItems] = useState<StoreProductCard[]>([])
  const [loading, setLoading] = useState(true)
  const [q, setQ] = useState('')

  useEffect(() => {
    storeListProducts()
      .then((all: any[]) => setItems(all.filter((p: any) => p.published)))
      .catch(() => {})
      .finally(() => setLoading(false))
  }, [])

  const filtered = items.filter((p) => !q || p.name.toLowerCase().includes(q.toLowerCase()))
  const featured = items.filter((p) => p.featured)
  const money = (v: number) => `$${Number(v ?? 0).toFixed(2)}`

  return (
    <div className="animate-fadeIn">
      {/* Hero */}
      <section className="relative overflow-hidden" style={{ background: 'var(--bg-hero)' }}>
        <div className="absolute inset-0 overflow-hidden opacity-40">
          <div className="absolute -top-1/2 -right-1/4 w-96 h-96 rounded-full blur-3xl" style={{ background: 'radial-gradient(circle, rgba(59,130,246,0.3) 0%, transparent 70%)' }} />
          <div className="absolute -bottom-1/4 -left-1/4 w-96 h-96 rounded-full blur-3xl" style={{ background: 'radial-gradient(circle, rgba(99,102,241,0.2) 0%, transparent 70%)' }} />
        </div>

        <div className="relative max-w-screen-2xl mx-auto px-6 sm:px-8 lg:px-16 py-24 sm:py-32 lg:py-48">
          <div className="max-w-3xl">
            {/* Badge */}
            <div className="inline-flex items-center gap-2.5 px-4 py-2 rounded-full text-sm font-semibold mb-8 animate-fadeIn"
              style={{ background: 'rgba(59,130,246,0.15)', color: 'rgba(59,130,246,1)', backdropFilter: 'blur(20px)', border: '1px solid rgba(59,130,246,0.3)' }}>
              <Zap size={15} className="flex-shrink-0" />
              <span>Sistema ERP Multi-Tenant</span>
            </div>

            {/* Heading */}
            <h1 className="text-4xl sm:text-5xl lg:text-6xl xl:text-7xl font-black text-white leading-tight mb-8 tracking-tight">
              Soluciones ERP para{' '}
              <span className="relative inline-block">
                <span style={{ color: '#3b82f6' }}>energía solar</span>
                <div className="absolute -inset-1 opacity-30 blur-md" style={{ background: '#3b82f6' }} />
              </span>
            </h1>

            {/* Description */}
            <p className="text-lg sm:text-xl text-white/75 leading-relaxed mb-12 max-w-2xl font-medium">
              Gestiona tu catálogo de paneles solares, inversores y estructuras. Sincronización automática con Odoo, tienda web integrada y análisis en tiempo real.
            </p>

            {/* Search Bar */}
            <div className="max-w-2xl mb-12">
              <div className="relative group">
                <div className="absolute inset-0 rounded-2xl opacity-0 group-hover:opacity-100 transition-opacity duration-300 blur-xl"
                  style={{ background: 'linear-gradient(135deg, rgba(59,130,246,0.5), rgba(99,102,241,0.3))' }} />
                <div className="relative flex items-center">
                  <Search size={20} className="absolute left-5 text-gray-400 pointer-events-none" />
                  <input
                    placeholder="Buscar paneles, inversores, estructuras..."
                    value={q}
                    onChange={(e) => setQ(e.target.value)}
                    className="w-full pl-14 pr-40 py-4 sm:py-5 text-sm sm:text-base rounded-2xl focus:outline-none focus:ring-2 transition-all duration-200"
                    style={{ background: 'rgba(255,255,255,0.98)', '--tw-ring-color': '#3b82f6' } as any}
                  />
                  <button className="absolute right-2 premium-btn-cta px-6 sm:px-8 py-2.5 sm:py-3 text-sm font-semibold rounded-xl">
                    Buscar
                  </button>
                </div>
              </div>
            </div>

            {/* Trust Badges */}
            <div className="flex flex-col sm:flex-row flex-wrap gap-6 sm:gap-8">
              {[
                { icon: Shield, text: 'Garantía 25 años' },
                { icon: Truck, text: 'Envío a nivel nacional' },
                { icon: Zap, text: 'Soporte 24/7' },
              ].map((badge, i) => (
                <div key={i} className="flex items-center gap-3 text-white/70 hover:text-white/90 transition-colors">
                  <div className="p-2 rounded-lg" style={{ background: 'rgba(255,255,255,0.08)' }}>
                    <badge.icon size={20} />
                  </div>
                  <span className="font-medium text-sm sm:text-base">{badge.text}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </section>

      {/* Stats bar */}
      <section className="py-16 sm:py-20 px-6 sm:px-8 lg:px-16" style={{ background: 'var(--bg-secondary)', borderTop: '1px solid var(--border-primary)', borderBottom: '1px solid var(--border-primary)' }}>
        <div className="max-w-screen-2xl mx-auto">
          <div className="grid grid-cols-2 md:grid-cols-4 gap-6 md:gap-8">
            {[
              { value: items.length, label: 'Productos en catálogo' },
              { value: featured.length, label: 'Destacados' },
              { value: '25+', label: 'Años de garantía' },
              { value: '24/7', label: 'Atención al cliente' },
            ].map((stat, i) => (
              <div key={i} className="text-center">
                <p className="text-3xl sm:text-4xl lg:text-5xl font-bold" style={{ color: 'var(--accent-primary)' }}>
                  {stat.value}
                </p>
                <p className="text-xs sm:text-sm font-medium mt-2.5" style={{ color: 'var(--text-tertiary)' }}>
                  {stat.label}
                </p>
              </div>
            ))}
          </div>
        </div>
      </section>

      {/* Featured */}
      {featured.length > 0 && !q && (
        <section className="max-w-screen-2xl mx-auto px-6 sm:px-8 lg:px-16 py-20 sm:py-28">
          <div className="mb-14 sm:mb-16">
            <div className="inline-flex items-center gap-2.5 px-3.5 py-1.5 rounded-full text-xs font-semibold mb-4"
              style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}>
              ⭐ Recomendados
            </div>
            <h2 className="text-3xl sm:text-4xl lg:text-5xl font-bold" style={{ color: 'var(--text-primary)' }}>Productos destacados</h2>
            <p className="text-base sm:text-lg mt-3" style={{ color: 'var(--text-secondary)' }}>Los más populares entre nuestros clientes</p>
          </div>
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 sm:gap-8">
            {featured.slice(0, 4).map((p, i) => (
              <ProductCard key={p.odoo_product_id} product={p} money={money} onClick={() => navigate(`/tienda/${p.odoo_product_id}`)} index={i} />
            ))}
          </div>
        </section>
      )}

      {/* All products */}
      <section className="max-w-screen-2xl mx-auto px-6 sm:px-8 lg:px-16 py-20 sm:py-28">
        <div className="mb-14 sm:mb-16">
          <h2 className="text-3xl sm:text-4xl lg:text-5xl font-bold" style={{ color: 'var(--text-primary)' }}>
            {q ? `Resultados para "${q}"` : 'Catálogo completo'}
          </h2>
          <p className="text-base sm:text-lg mt-3" style={{ color: 'var(--text-secondary)' }}>
            <span style={{ color: 'var(--accent-primary)', fontWeight: '600' }}>{filtered.length}</span> producto{filtered.length !== 1 ? 's' : ''} disponible{filtered.length !== 1 ? 's' : ''}
          </p>
        </div>

        {loading ? (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 sm:gap-8">
            {[...Array(8)].map((_, i) => (
              <div key={i} className="rounded-2xl overflow-hidden" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
                <div className="aspect-square animate-pulse" style={{ background: 'var(--bg-tertiary)' }} />
                <div className="p-6 space-y-3">
                  <div className="h-4 animate-pulse rounded" style={{ background: 'var(--bg-tertiary)' }} />
                  <div className="h-3 animate-pulse rounded w-2/3" style={{ background: 'var(--bg-tertiary)' }} />
                  <div className="h-5 animate-pulse rounded w-1/3" style={{ background: 'var(--bg-tertiary)' }} />
                </div>
              </div>
            ))}
          </div>
        ) : filtered.length === 0 ? (
          <div className="text-center py-28 sm:py-32 rounded-2xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
            <div className="w-24 h-24 sm:w-32 sm:h-32 rounded-2xl flex items-center justify-center mx-auto mb-6 sm:mb-8" style={{ background: 'var(--bg-tertiary)' }}>
              <Package size={40} style={{ color: 'var(--text-tertiary)' }} />
            </div>
            <h3 className="text-xl sm:text-2xl font-bold mb-2 sm:mb-3" style={{ color: 'var(--text-primary)' }}>
              {q ? 'Sin resultados' : 'Catálogo vacío'}
            </h3>
            <p className="text-sm sm:text-base" style={{ color: 'var(--text-tertiary)' }}>
              {q ? `No encontramos productos que coincidan con "${q}"` : 'Los productos estarán disponibles pronto.'}
            </p>
          </div>
        ) : (
          <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 sm:gap-8">
            {filtered.map((p, i) => (
              <ProductCard key={p.odoo_product_id} product={p} money={money} onClick={() => navigate(`/tienda/${p.odoo_product_id}`)} index={i} />
            ))}
          </div>
        )}
      </section>

      {/* CTA Section */}
      <section className="px-6 sm:px-8 lg:px-16 py-24 sm:py-32">
        <div className="max-w-screen-2xl mx-auto">
          <div className="rounded-3xl sm:rounded-4xl p-8 sm:p-12 lg:p-20 text-center relative overflow-hidden"
            style={{ background: 'linear-gradient(135deg, #3b82f6, #1e40af)' }}>
            <div className="absolute inset-0 opacity-20">
              <div className="absolute -top-1/2 -right-1/4 w-96 h-96 rounded-full blur-3xl" style={{ background: 'white' }} />
              <div className="absolute -bottom-1/2 -left-1/4 w-96 h-96 rounded-full blur-3xl" style={{ background: 'white' }} />
            </div>
            <div className="relative">
              <h2 className="text-3xl sm:text-4xl lg:text-5xl font-bold text-white mb-4 sm:mb-6 leading-tight">
                ¿Listo para transformar tu negocio?
              </h2>
              <p className="text-base sm:text-lg text-white/85 mb-8 sm:mb-10 max-w-2xl mx-auto leading-relaxed">
                Contacta con nuestro equipo de especialistas para una consulta personalizada sin costo. Te ayudaremos a encontrar la solución perfecta para tu empresa.
              </p>
              <button className="premium-btn-cta px-8 sm:px-12 py-3.5 sm:py-4 text-base sm:text-lg font-semibold rounded-xl inline-flex items-center gap-2.5 hover:scale-105 transition-transform">
                Solicitar demo gratuita
                <ArrowRight size={20} />
              </button>
            </div>
          </div>
        </div>
      </section>
    </div>
  )
}

function ProductCard({ product, money, onClick, index }: { product: StoreProductCard; money: (v: number) => string; onClick: () => void; index: number }) {
  return (
    <div
      onClick={onClick}
      className="group rounded-2xl overflow-hidden cursor-pointer transition-all duration-300 hover:-translate-y-1 hover:shadow-2xl"
      style={{
        background: 'var(--bg-card)',
        border: '1px solid var(--border-primary)',
        boxShadow: 'var(--shadow-sm)',
      }}>
      {/* Image Container */}
      <div className="relative aspect-square overflow-hidden" style={{ background: 'var(--bg-tertiary)' }}>
        {product.image ? (
          <img
            src={product.image}
            alt={product.name}
            className="w-full h-full object-contain p-8 sm:p-10 transition-transform duration-500 group-hover:scale-105"
          />
        ) : (
          <div className="w-full h-full flex items-center justify-center">
            <ImageIcon size={56} style={{ color: 'var(--text-tertiary)' }} />
          </div>
        )}

        {/* Overlay gradient on hover */}
        <div className="absolute inset-0 bg-gradient-to-t from-black/10 to-transparent opacity-0 group-hover:opacity-100 transition-opacity duration-300" />

        {/* Featured Badge */}
        {product.featured && (
          <span className="absolute top-4 left-4 flex items-center gap-1 text-xs font-bold px-3 py-1.5 rounded-lg text-white shadow-md"
            style={{ background: 'linear-gradient(135deg, #f59e0b, #f97316)' }}>
            <Star size={13} fill="currentColor" />
            <span>Destacado</span>
          </span>
        )}

        {/* Image count */}
        {product.image_count > 1 && (
          <span className="absolute bottom-4 right-4 text-xs font-semibold px-3 py-1.5 rounded-lg text-white shadow-md"
            style={{ background: 'rgba(0, 0, 0, 0.7)', backdropFilter: 'blur(10px)' }}>
            +{product.image_count - 1}
          </span>
        )}
      </div>

      {/* Content */}
      <div className="p-5 sm:p-6 flex flex-col">
        {/* SKU */}
        {product.sku && (
          <p className="text-xs font-mono mb-3 uppercase tracking-wide" style={{ color: 'var(--text-tertiary)' }}>
            {product.sku}
          </p>
        )}

        {/* Title */}
        <h3
          className="text-base font-semibold line-clamp-2 mb-3 sm:mb-4 leading-tight transition-colors group-hover:text-[var(--accent-primary)] flex-grow"
          style={{ color: 'var(--text-primary)' }}>
          {product.name}
        </h3>

        {/* Price & Stock */}
        <div className="flex items-end justify-between gap-3">
          <div className="flex-1">
            <p className="text-2xl sm:text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>
              {money(product.price)}
            </p>
            <p
              className="text-xs sm:text-sm font-semibold mt-2 uppercase tracking-wide"
              style={{ color: Number(product.stock) > 0 ? 'var(--success)' : 'var(--danger)' }}>
              {Number(product.stock) > 0 ? `${Number(product.stock)} en stock` : 'Agotado'}
            </p>
          </div>
          <button
            className="w-12 h-12 sm:w-14 sm:h-14 rounded-xl flex items-center justify-center transition-all duration-200 group-hover:scale-110 flex-shrink-0"
            style={{ background: 'var(--accent-bg)', color: 'var(--accent-primary)' }}
            title="Ver detalles"
            onClick={(e) => e.stopPropagation()}>
            <ShoppingCart size={22} />
          </button>
        </div>
      </div>
    </div>
  )
}
