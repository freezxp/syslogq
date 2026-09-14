import { NavLink } from 'react-router-dom'
import { useAuth } from '../lib/auth'

const NAV = [
  { to: '/explorer', label: 'Explorer', icon: '⌕' },
  { to: '/dashboard', label: 'Dashboard', icon: '▤' },
  { to: '/system', label: 'System', icon: '⚙' },
]

export default function Layout({ children }: { children: React.ReactNode }) {
  const { user, logout } = useAuth()
  return (
    <div className="flex h-full">
      <aside className="flex w-52 shrink-0 flex-col border-r border-line bg-panel">
        <div className="flex items-center gap-2 px-4 py-4">
          <span className="flex h-7 w-7 items-center justify-center rounded bg-accent/15 font-mono text-sm text-accent">
            sq
          </span>
          <span className="font-semibold tracking-tight">syslogq</span>
        </div>
        <nav className="flex flex-col gap-0.5 px-2">
          {NAV.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              className={({ isActive }) =>
                `flex items-center gap-2.5 rounded-md px-3 py-1.5 text-[13px] ${
                  isActive
                    ? 'bg-accent/12 text-accent'
                    : 'text-muted hover:bg-panel-2 hover:text-text'
                }`
              }
            >
              <span className="w-4 text-center text-xs opacity-80">{item.icon}</span>
              {item.label}
            </NavLink>
          ))}
        </nav>
        <div className="mt-auto border-t border-line px-4 py-3 text-xs">
          <div className="truncate font-medium">{user?.username ?? '—'}</div>
          <div className="flex items-center justify-between text-muted">
            <span className="capitalize">{user?.role}</span>
            <button
              onClick={() => void logout()}
              className="rounded px-1.5 py-0.5 hover:bg-panel-2 hover:text-text"
            >
              Sign out
            </button>
          </div>
        </div>
      </aside>
      <main className="min-w-0 flex-1 overflow-hidden">{children}</main>
    </div>
  )
}
