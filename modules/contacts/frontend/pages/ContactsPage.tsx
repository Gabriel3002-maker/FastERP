import { useEffect, useState } from 'react'
import { Plus, Trash2, Search, Users, User, Building2, CreditCard, FileText, MapPin, Globe, Pencil, X, Loader2 } from 'lucide-react'

interface Contact {
  id: string
  name: string
  email: string
  phone: string
  mobile: string
  company: string
  job_title: string
  tax_id_type: string
  tax_id: string
  person_type: string
  tax_regime: string
  accounting_obligation: string
  retention_agent: string
  address: string
  city: string
  province: string
  country: string
  postal_code: string
  website: string
  notes: string
  created_at: string
}

type Tab = 'personal' | 'fiscal' | 'regime' | 'address' | 'other'

const TABS: { key: Tab; label: string; icon: React.ReactNode }[] = [
  { key: 'personal', label: 'Datos Personales', icon: <User size={16} /> },
  { key: 'fiscal', label: 'Identificación Fiscal', icon: <CreditCard size={16} /> },
  { key: 'regime', label: 'Régimen Tributario', icon: <FileText size={16} /> },
  { key: 'address', label: 'Dirección', icon: <MapPin size={16} /> },
  { key: 'other', label: 'Otros', icon: <Globe size={16} /> },
]

const TAX_ID_TYPES = [
  { value: 'cedula', label: 'Cédula (Ecuador)' },
  { value: 'ruc', label: 'RUC (Ecuador)' },
  { value: 'rfc', label: 'RFC (México)' },
  { value: 'curp', label: 'CURP (México)' },
  { value: 'pasaporte', label: 'Pasaporte' },
  { value: 'dni', label: 'DNI' },
  { value: 'otro', label: 'Otro' },
]

const PERSON_TYPES = [
  { value: 'natural', label: 'Persona Natural' },
  { value: 'juridica', label: 'Persona Jurídica' },
]

const TAX_REGIMES_EC = [
  { value: 'general', label: 'Régimen General' },
  { value: 'rimpe', label: 'RIMPE (Régimen Simplificado)' },
  { value: 'microempresas', label: 'Microempresas' },
  { value: 'sociedades', label: 'Sociedades' },
  { value: 'sin_renta', label: 'Sin fines de lucro' },
]

const TAX_REGIMES_MX = [
  { value: 'general', label: 'Régimen General de Ley' },
  { value: 'simplificado', label: 'Régimen Simplificado de Confianza (RESICO)' },
  { value: 'actividad_empresarial', label: 'Actividades Empresariales y Profesionales' },
  { value: 'arrendamiento', label: 'Arrendamiento' },
  { value: 'sueldos', label: 'Sueldos y Salarios' },
  { value: 'dividendos', label: 'Dividendos' },
  { value: 'enajenacion', label: 'Enajenación de Bienes' },
]

const SI_NO = [
  { value: 'si', label: 'Sí' },
  { value: 'no', label: 'No' },
]

const emptyForm: Record<string, string> = {
  name: '', email: '', phone: '', mobile: '', company: '', job_title: '',
  tax_id_type: '', tax_id: '', person_type: '', tax_regime: '',
  accounting_obligation: '', retention_agent: '', address: '', city: '',
  province: '', country: '', postal_code: '', website: '', notes: '',
}

export default function ContactsPage() {
  const [contacts, setContacts] = useState<Contact[]>([])
  const [loading, setLoading] = useState(true)
  const [showForm, setShowForm] = useState(false)
  const [editId, setEditId] = useState<string | null>(null)
  const [form, setForm] = useState<Record<string, string>>({ ...emptyForm })
  const [search, setSearch] = useState('')
  const [saving, setSaving] = useState(false)
  const [activeTab, setActiveTab] = useState<Tab>('personal')

  const ah = () => ({
    'Content-Type': 'application/json',
    'Authorization': 'Bearer ' + localStorage.getItem('fasterp_token'),
    'X-Tenant-ID': localStorage.getItem('fasterp_tenant_id') || '',
  })

  const fetchContacts = async () => {
    try {
      const res = await fetch('/api/contacts/contact', { headers: ah() })
      const data = await res.json()
      setContacts(Array.isArray(data) ? data : [])
    } catch (e) {
      console.error(e)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { fetchContacts() }, [])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    setSaving(true)
    try {
      const method = editId ? 'PUT' : 'POST'
      const url = editId ? `/api/contacts/contact/${editId}` : '/api/contacts/contact'
      await fetch(url, { method, headers: ah(), body: JSON.stringify(form) })
      setForm({ ...emptyForm })
      setShowForm(false)
      setEditId(null)
      setActiveTab('personal')
      fetchContacts()
    } catch (e) {
      console.error(e)
    } finally {
      setSaving(false)
    }
  }

  const handleEdit = (c: Contact) => {
    const f: Record<string, string> = {}
    Object.keys(emptyForm).forEach((k) => { f[k] = (c as any)[k] || '' })
    setForm(f)
    setEditId(c.id)
    setShowForm(true)
    setActiveTab('personal')
  }

  const handleDelete = async (id: string) => {
    if (!confirm('¿Eliminar este contacto?')) return
    try {
      await fetch(`/api/contacts/contact/${id}`, { method: 'DELETE', headers: ah() })
      fetchContacts()
    } catch (e) {
      console.error(e)
    }
  }

  const filtered = contacts.filter((c: Contact) =>
    !search || `${c.name} ${c.email} ${c.company} ${c.tax_id} ${c.city}`.toLowerCase().includes(search.toLowerCase())
  )

  const updateField = (key: string, value: string) => setForm((prev: any) => ({ ...prev, [key]: value }))

  const inputStyle = {
    background: 'var(--bg-input)',
    border: '1px solid var(--border-primary)',
    color: 'var(--text-primary)',
  }

  const renderField = (key: string, label: string, placeholder: string, type = 'text', required = false) => (
    <div>
      <label className="block text-xs font-semibold uppercase tracking-wider mb-2" style={{ color: 'var(--text-secondary)' }}>
        {label} {required && <span className="text-red-500">*</span>}
      </label>
      <input
        type={type}
        value={form[key] || ''}
        onChange={(e) => updateField(key, e.target.value)}
        placeholder={placeholder}
        className="w-full px-4 py-3 rounded-xl text-sm transition-all duration-150 focus:outline-none focus:ring-2 focus:ring-blue-500"
        style={inputStyle}
        required={required}
      />
    </div>
  )

  const renderSelect = (key: string, label: string, options: { value: string; label: string }[], required = false) => (
    <div>
      <label className="block text-xs font-semibold uppercase tracking-wider mb-2" style={{ color: 'var(--text-secondary)' }}>
        {label} {required && <span className="text-red-500">*</span>}
      </label>
      <select
        value={form[key] || ''}
        onChange={(e) => updateField(key, e.target.value)}
        className="w-full px-4 py-3 rounded-xl text-sm transition-all duration-150 focus:outline-none focus:ring-2 focus:ring-blue-500 appearance-none"
        style={{ ...inputStyle, backgroundImage: `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='%236b7280' stroke-width='2'%3E%3Cpolyline points='6 9 12 15 18 9'/%3E%3C/svg%3E")`, backgroundRepeat: 'no-repeat', backgroundPosition: 'right 12px center' }}
        required={required}
      >
        <option value="">Seleccionar...</option>
        {options.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>
    </div>
  )

  const renderTabContent = () => {
    switch (activeTab) {
      case 'personal':
        return (
          <div className="grid sm:grid-cols-2 gap-5">
            {renderField('name', 'Nombre Completo', 'Juan Pérez', 'text', true)}
            {renderField('email', 'Email', 'correo@empresa.com', 'email')}
            {renderField('phone', 'Teléfono', '+593 2 123 4567')}
            {renderField('mobile', 'Celular', '+593 99 123 4567')}
            {renderField('company', 'Empresa', 'Nombre de la empresa')}
            {renderField('job_title', 'Cargo', 'Gerente General')}
          </div>
        )
      case 'fiscal':
        return (
          <div className="grid sm:grid-cols-2 gap-5">
            {renderSelect('person_type', 'Tipo de Persona', PERSON_TYPES, true)}
            {renderSelect('tax_id_type', 'Tipo de Identificación', TAX_ID_TYPES, true)}
            {renderField('tax_id', 'Número de Identificación', '1712345678001')}
            {form.person_type === 'natural' && form.country === 'ec' && (
              <>
                {renderField('tax_id', 'Cédula', '1712345678')}
              </>
            )}
          </div>
        )
      case 'regime':
        return (
          <div className="grid sm:grid-cols-2 gap-5">
            {renderSelect('tax_regime', 'Régimen Tributario', TAX_REGIMES_EC)}
            {renderSelect('accounting_obligation', 'Obligado a Llevar Contabilidad', SI_NO)}
            {renderSelect('retention_agent', 'Agente de Retención', SI_NO)}
          </div>
        )
      case 'address':
        return (
          <div className="grid sm:grid-cols-2 gap-5">
            <div className="sm:col-span-2">
              <label className="block text-xs font-semibold uppercase tracking-wider mb-2" style={{ color: 'var(--text-secondary)' }}>Dirección</label>
              <textarea
                value={form.address || ''}
                onChange={(e) => updateField('address', e.target.value)}
                placeholder="Av. Principal 123 y Calle Secundaria"
                rows={2}
                className="w-full px-4 py-3 rounded-xl text-sm transition-all duration-150 focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                style={inputStyle}
              />
            </div>
            {renderField('city', 'Ciudad', 'Quito / Ciudad de México')}
            {renderField('province', 'Provincia / Estado', 'Pichincha / CDMX')}
            {renderField('country', 'País', 'Ecuador / México')}
            {renderField('postal_code', 'Código Postal', '170101')}
          </div>
        )
      case 'other':
        return (
          <div className="grid sm:grid-cols-2 gap-5">
            {renderField('website', 'Sitio Web', 'https://www.empresa.com', 'url')}
            <div className="sm:col-span-2">
              <label className="block text-xs font-semibold uppercase tracking-wider mb-2" style={{ color: 'var(--text-secondary)' }}>Notas</label>
              <textarea
                value={form.notes || ''}
                onChange={(e) => updateField('notes', e.target.value)}
                placeholder="Notas adicionales sobre el contacto..."
                rows={3}
                className="w-full px-4 py-3 rounded-xl text-sm transition-all duration-150 focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
                style={inputStyle}
              />
            </div>
          </div>
        )
    }
  }

  const getTaxLabel = (val: string) => {
    const all = [...TAX_ID_TYPES]
    return all.find((o) => o.value === val)?.label || val || '—'
  }

  return (
    <div className="space-y-8 animate-fadeIn">
      {/* Header */}
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
        <div className="flex items-center gap-4">
          <div className="w-12 h-12 rounded-xl flex items-center justify-center" style={{ background: 'linear-gradient(135deg, #3b82f6, #1e40af)' }}>
            <Users size={24} className="text-white" />
          </div>
          <div>
            <h1 className="text-3xl font-bold" style={{ color: 'var(--text-primary)' }}>Contactos</h1>
            <p className="text-sm mt-0.5" style={{ color: 'var(--text-tertiary)' }}>
              {contacts.length} contacto{contacts.length !== 1 ? 's' : ''} registrado{contacts.length !== 1 ? 's' : ''}
            </p>
          </div>
        </div>
        <button
          onClick={() => { setShowForm(!showForm); setEditId(null); setForm({ ...emptyForm }); setActiveTab('personal') }}
          className="premium-btn px-5 py-3 text-sm font-semibold flex items-center gap-2"
        >
          {showForm ? <X size={18} /> : <Plus size={18} />}
          {showForm ? 'Cerrar' : 'Nuevo Contacto'}
        </button>
      </div>

      {/* Form Panel */}
      {showForm && (
        <div className="rounded-2xl overflow-hidden transition-all duration-300" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)', boxShadow: 'var(--shadow-lg)' }}>
          {/* Tabs */}
          <div className="flex overflow-x-auto border-b" style={{ borderColor: 'var(--border-primary)', background: 'var(--bg-tertiary)' }}>
            {TABS.map((tab) => (
              <button
                key={tab.key}
                onClick={() => setActiveTab(tab.key)}
                className="flex items-center gap-2 px-5 py-4 text-sm font-medium whitespace-nowrap transition-all duration-150 border-b-2"
                style={{
                  color: activeTab === tab.key ? '#3b82f6' : 'var(--text-tertiary)',
                  borderBottomColor: activeTab === tab.key ? '#3b82f6' : 'transparent',
                  background: activeTab === tab.key ? 'var(--bg-card)' : 'transparent',
                }}
              >
                {tab.icon}
                {tab.label}
              </button>
            ))}
          </div>

          {/* Tab Content */}
          <div className="p-8">
            {renderTabContent()}
          </div>

          {/* Actions */}
          <div className="px-8 pb-8 flex gap-3">
            <button onClick={handleSubmit} disabled={saving} className="premium-btn px-6 py-3 text-sm font-semibold flex items-center gap-2">
              {saving ? <Loader2 size={16} className="animate-spin" /> : <Plus size={16} />}
              {saving ? 'Guardando...' : editId ? 'Actualizar Contacto' : 'Crear Contacto'}
            </button>
            <button
              type="button"
              onClick={() => { setShowForm(false); setEditId(null); setForm({ ...emptyForm }) }}
              className="px-5 py-3 text-sm font-semibold rounded-xl transition-all"
              style={{ background: 'var(--bg-input)', border: '1px solid var(--border-primary)', color: 'var(--text-primary)' }}
            >
              Cancelar
            </button>
          </div>
        </div>
      )}

      {/* Search */}
      <div className="relative">
        <Search size={18} className="absolute left-4 top-1/2 -translate-y-1/2" style={{ color: 'var(--text-tertiary)' }} />
        <input
          placeholder="Buscar por nombre, email, empresa, RUC o ciudad..."
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          className="w-full pl-12 pr-4 py-3.5 rounded-xl text-sm transition-all focus:outline-none focus:ring-2 focus:ring-blue-500"
          style={{ background: 'var(--bg-input)', border: '1px solid var(--border-primary)', color: 'var(--text-primary)' }}
        />
      </div>

      {/* Table */}
      {loading ? (
        <div className="text-center py-16 rounded-2xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <Loader2 size={32} className="animate-spin mx-auto mb-3" style={{ color: 'var(--text-tertiary)' }} />
          <p className="text-sm" style={{ color: 'var(--text-tertiary)' }}>Cargando contactos...</p>
        </div>
      ) : filtered.length === 0 ? (
        <div className="text-center py-16 rounded-2xl" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)' }}>
          <div className="w-16 h-16 rounded-2xl flex items-center justify-center mx-auto mb-4" style={{ background: 'var(--bg-tertiary)' }}>
            <Users size={28} style={{ color: 'var(--text-tertiary)' }} />
          </div>
          <p className="font-semibold text-lg" style={{ color: 'var(--text-primary)' }}>
            {search ? 'Sin resultados' : 'Sin contactos'}
          </p>
          <p className="text-sm mt-1.5" style={{ color: 'var(--text-tertiary)' }}>
            {search ? 'Intenta con otros términos de búsqueda' : 'Crea tu primer contacto para comenzar'}
          </p>
        </div>
      ) : (
        <div className="rounded-2xl overflow-hidden" style={{ background: 'var(--bg-card)', border: '1px solid var(--border-primary)', boxShadow: 'var(--shadow-md)' }}>
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead style={{ background: 'var(--bg-tertiary)', borderBottom: '1px solid var(--border-primary)' }}>
                <tr>
                  <th className="px-6 py-4 text-left font-semibold" style={{ color: 'var(--text-primary)' }}>Nombre</th>
                  <th className="px-6 py-4 text-left font-semibold hidden md:table-cell" style={{ color: 'var(--text-primary)' }}>Identificación</th>
                  <th className="px-6 py-4 text-left font-semibold hidden lg:table-cell" style={{ color: 'var(--text-primary)' }}>Email</th>
                  <th className="px-6 py-4 text-left font-semibold hidden lg:table-cell" style={{ color: 'var(--text-primary)' }}>Teléfono</th>
                  <th className="px-6 py-4 text-left font-semibold hidden xl:table-cell" style={{ color: 'var(--text-primary)' }}>Empresa</th>
                  <th className="px-6 py-4 text-left font-semibold hidden xl:table-cell" style={{ color: 'var(--text-primary)' }}>Ciudad</th>
                  <th className="px-6 py-4 text-right font-semibold" style={{ color: 'var(--text-primary)' }}>Acciones</th>
                </tr>
              </thead>
              <tbody>
                {filtered.map((c: Contact, i: number) => (
                  <tr
                    key={c.id}
                    className="transition-colors duration-100"
                    style={{ borderBottom: i < filtered.length - 1 ? '1px solid var(--border-primary)' : 'none' }}
                    onMouseEnter={(e: any) => (e.currentTarget.style.background = 'var(--bg-tertiary)')}
                    onMouseLeave={(e: any) => (e.currentTarget.style.background = 'transparent')}
                  >
                    <td className="px-6 py-4">
                      <div className="flex items-center gap-3">
                        <div className="w-9 h-9 rounded-lg flex items-center justify-center text-xs font-bold text-white shrink-0"
                          style={{ background: 'linear-gradient(135deg, #3b82f6, #1e40af)' }}>
                          {c.name?.split(' ').slice(0, 2).map((w: string) => w[0]).join('').toUpperCase()}
                        </div>
                        <div>
                          <div className="font-semibold" style={{ color: 'var(--text-primary)' }}>{c.name}</div>
                          {c.job_title && <div className="text-xs" style={{ color: 'var(--text-tertiary)' }}>{c.job_title}</div>}
                        </div>
                      </div>
                    </td>
                    <td className="px-6 py-4 hidden md:table-cell">
                      {c.tax_id ? (
                        <div>
                          <span className="text-xs font-medium px-2 py-0.5 rounded-md" style={{ background: 'var(--bg-tertiary)', color: 'var(--text-secondary)' }}>
                            {getTaxLabel(c.tax_id_type)}
                          </span>
                          <div className="text-sm mt-1 font-mono" style={{ color: 'var(--text-primary)' }}>{c.tax_id}</div>
                        </div>
                      ) : <span style={{ color: 'var(--text-tertiary)' }}>—</span>}
                    </td>
                    <td className="px-6 py-4 hidden lg:table-cell text-sm" style={{ color: 'var(--text-tertiary)' }}>{c.email || '—'}</td>
                    <td className="px-6 py-4 hidden lg:table-cell text-sm" style={{ color: 'var(--text-tertiary)' }}>{c.phone || c.mobile || '—'}</td>
                    <td className="px-6 py-4 hidden xl:table-cell text-sm" style={{ color: 'var(--text-tertiary)' }}>{c.company || '—'}</td>
                    <td className="px-6 py-4 hidden xl:table-cell text-sm" style={{ color: 'var(--text-tertiary)' }}>
                      {[c.city, c.province].filter(Boolean).join(', ') || '—'}
                    </td>
                    <td className="px-6 py-4 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => handleEdit(c)}
                          className="p-2 rounded-lg transition-colors"
                          style={{ color: 'var(--text-tertiary)' }}
                          title="Editar"
                        >
                          <Pencil size={16} />
                        </button>
                        <button
                          onClick={() => handleDelete(c.id)}
                          className="p-2 rounded-lg transition-colors"
                          style={{ color: 'var(--text-tertiary)' }}
                          title="Eliminar"
                        >
                          <Trash2 size={16} />
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div className="px-6 py-3 text-xs" style={{ borderTop: '1px solid var(--border-primary)', color: 'var(--text-tertiary)', background: 'var(--bg-tertiary)' }}>
            {filtered.length} resultado{filtered.length !== 1 ? 's' : ''}
          </div>
        </div>
      )}
    </div>
  )
}
