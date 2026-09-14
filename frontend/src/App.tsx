import { Routes, Route, Navigate } from 'react-router-dom'
import { useAuth } from './lib/auth'
import Layout from './components/Layout'
import Login from './pages/Login'
import Explorer from './pages/Explorer'
import Dashboard from './pages/Dashboard'
import System from './pages/System'

function Protected({ children }: { children: React.ReactNode }) {
  const { user, loading } = useAuth()
  if (loading) {
    return <div className="flex h-full items-center justify-center text-muted">Loading…</div>
  }
  if (!user) return <Navigate to="/login" replace />
  return <Layout>{children}</Layout>
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route
        path="/explorer"
        element={
          <Protected>
            <Explorer />
          </Protected>
        }
      />
      <Route
        path="/dashboard"
        element={
          <Protected>
            <Dashboard />
          </Protected>
        }
      />
      <Route
        path="/system"
        element={
          <Protected>
            <System />
          </Protected>
        }
      />
      <Route path="/" element={<Navigate to="/explorer" replace />} />
      <Route path="*" element={<Navigate to="/explorer" replace />} />
    </Routes>
  )
}
