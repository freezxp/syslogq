import { useState } from 'react'
import { Navigate } from 'react-router-dom'
import { useAuth } from '../lib/auth'

export default function Login() {
  const { user, loading, login } = useAuth()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  if (user) return <Navigate to="/explorer" replace />
  if (loading) {
    return (
      <div className="flex h-full items-center justify-center text-muted">Loading…</div>
    )
  }

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError(null)
    try {
      await login(username, password)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'login failed')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex h-full items-center justify-center">
      <form
        onSubmit={submit}
        className="w-80 rounded-xl border border-line bg-panel p-6 shadow-xl"
      >
        <div className="mb-5 flex items-center gap-2.5">
          <span className="flex h-8 w-8 items-center justify-center rounded-lg bg-accent/15 font-mono text-sm text-accent">
            sq
          </span>
          <div>
            <div className="font-semibold tracking-tight">syslogq</div>
            <div className="text-xs text-muted">Log in to your instance</div>
          </div>
        </div>
        <label className="mb-3 block text-xs text-muted">
          Username
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
            required
            className="mt-1 w-full rounded-md border border-line bg-panel-2 px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent"
          />
        </label>
        <label className="mb-4 block text-xs text-muted">
          Password
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
            required
            className="mt-1 w-full rounded-md border border-line bg-panel-2 px-2.5 py-1.5 text-sm text-text outline-none focus:border-accent"
          />
        </label>
        {error && (
          <div className="mb-3 rounded-md bg-red-500/10 px-2.5 py-1.5 text-xs text-red-400">
            {error}
          </div>
        )}
        <button
          type="submit"
          disabled={busy}
          className="w-full rounded-md bg-accent py-1.5 text-sm font-medium text-black transition-colors hover:brightness-110 disabled:opacity-50"
        >
          {busy ? 'Signing in…' : 'Sign in'}
        </button>
      </form>
    </div>
  )
}
