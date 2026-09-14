export type Role = 'admin' | 'operator' | 'viewer'

export interface Me {
  username: string
  role: Role
  expires_at: string
}

export interface LogRow {
  [field: string]: string
}

export interface SearchResponse {
  logs: LogRow[]
  next_offset?: number
}

export interface ValueCount {
  value: string
  count: number
}

export interface VolumeResponse {
  bucket: string
  buckets: { time: string; count: number }[]
}

export interface IngestResponse {
  accepted: number
  rejected: Record<string, number>
}

export class ApiError extends Error {
  readonly status: number
  readonly code: string

  constructor(status: number, code: string, message: string) {
    super(message)
    this.status = status
    this.code = code
  }
}

const TOKEN_KEY = 'syslogq_token'

export function getToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function setToken(token: string | null) {
  if (token === null) {
    localStorage.removeItem(TOKEN_KEY)
  } else {
    localStorage.setItem(TOKEN_KEY, token)
  }
}

async function parseError(res: Response): Promise<ApiError> {
  try {
    const body = JSON.parse(await res.text()) as {
      error?: { code?: string; message?: string }
    }
    return new ApiError(
      res.status,
      body.error?.code ?? 'unknown',
      body.error?.message ?? res.statusText,
    )
  } catch {
    return new ApiError(res.status, 'unknown', res.statusText)
  }
}

async function request<T>(
  method: string,
  path: string,
  opts: { body?: unknown; raw?: boolean; signal?: AbortSignal } = {},
): Promise<T> {
  const headers: Record<string, string> = {}
  const token = getToken()
  if (token) headers['Authorization'] = `Bearer ${token}`
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json'
  const res = await fetch(path, {
    method,
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    signal: opts.signal,
  })
  if (!res.ok) throw await parseError(res)
  if (opts.raw) return res as T
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export function get<T>(path: string, signal?: AbortSignal): Promise<T> {
  return request<T>('GET', path, { signal })
}

export const api = {
  login: (username: string, password: string) =>
    request<{ token: string; expires_at: string; user: { username: string; role: Role } }>(
      'POST',
      '/api/v1/auth/login',
      { body: { username, password } },
    ),
  logout: () => request<{ status: string }>('POST', '/api/v1/auth/logout'),
  me: (signal?: AbortSignal) => request<Me>('GET', '/api/v1/auth/me', { signal }),
  systemInfo: (signal?: AbortSignal) =>
    request<{ version: string }>('GET', '/api/v1/system/info', { signal }),

  search: (
    params: { q?: string; start: string; end: string; limit?: number; offset?: number },
    signal?: AbortSignal,
  ) =>
    get<SearchResponse>(
      `/api/v1/logs/search?${new URLSearchParams(
        cleanParams(params) as Record<string, string>,
      )}`,
      signal,
    ),
  count: (params: { q?: string; start: string; end: string }, signal?: AbortSignal) =>
    get<{ count: number }>(
      `/api/v1/logs/count?${new URLSearchParams(cleanParams(params) as Record<string, string>)}`,
      signal,
    ),
  volume: (
    params: { q?: string; start: string; end: string; bucket: string },
    signal?: AbortSignal,
  ) =>
    get<VolumeResponse>(
      `/api/v1/logs/volume?${new URLSearchParams(cleanParams(params) as Record<string, string>)}`,
      signal,
    ),
  fields: (params: { q?: string; start: string; end: string }, signal?: AbortSignal) =>
    get<{ fields: ValueCount[] }>(
      `/api/v1/fields?${new URLSearchParams(cleanParams(params) as Record<string, string>)}`,
      signal,
    ),
  fieldValues: (
    field: string,
    params: { q?: string; start: string; end: string; limit?: number },
    signal?: AbortSignal,
  ) =>
    get<{ field: string; values: ValueCount[] }>(
      `/api/v1/fields/${encodeURIComponent(field)}/values?${new URLSearchParams(
        cleanParams(params) as Record<string, string>,
      )}`,
      signal,
    ),

  exportUrl: (params: { q?: string; start: string; end: string; format: string; limit?: number }) =>
    `/api/v1/logs/export?${new URLSearchParams(cleanParams(params) as Record<string, string>)}`,
  exportBlob: async (
    params: { q?: string; start: string; end: string; format: string; limit?: number },
  ): Promise<Blob> => {
    const res = await request<Response>('GET', api.exportUrl(params), { raw: true })
    return res.blob()
  },

  health: (signal?: AbortSignal) => get<{ status: string }>('/health', signal),
  ready: (signal?: AbortSignal) => request<Record<string, unknown>>('GET', '/ready', { signal }),
  metrics: async (signal?: AbortSignal): Promise<string> => {
    const res = await request<Response>('GET', '/metrics', { raw: true, signal })
    return res.text()
  },
}

function cleanParams(params: Record<string, unknown>): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '' && v !== null) out[k] = String(v)
  }
  return out
}
