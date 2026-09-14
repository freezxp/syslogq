import { createContext, useCallback, useContext, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { api, getToken, setToken } from './api'
import type { Me } from './api'

interface AuthState {
  user: Me | null
  loading: boolean
  login: (username: string, password: string) => Promise<void>
  logout: () => Promise<void>
}

const AuthContext = createContext<AuthState | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const queryClient = useQueryClient()
  const [hasToken, setHasToken] = useState<boolean>(() => getToken() !== null)

  const meQuery = useQuery({
    queryKey: ['me'],
    queryFn: ({ signal }) => api.me(signal),
    enabled: hasToken,
    retry: false,
    staleTime: 5 * 60_000,
  })

  const login = useCallback(
    async (username: string, password: string) => {
      const res = await api.login(username, password)
      setToken(res.token)
      setHasToken(true)
      await queryClient.invalidateQueries()
    },
    [queryClient],
  )

  const logout = useCallback(async () => {
    try {
      await api.logout()
    } catch {
      // session may already be gone; clear locally regardless
    }
    setToken(null)
    setHasToken(false)
    queryClient.clear()
  }, [queryClient])

  const value = useMemo<AuthState>(
    () => ({
      user: meQuery.data ?? null,
      loading: hasToken && meQuery.isPending,
      login,
      logout,
    }),
    [meQuery.data, meQuery.isPending, hasToken, login, logout],
  )

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth outside AuthProvider')
  return ctx
}
