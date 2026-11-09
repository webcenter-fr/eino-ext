export interface ApprovalSummary {
  approvalId: string
  serverName: string
  toolName: string
  instance?: string
  requesterSub: string
  requester?: string
  createdAt: string
  expiresAt: string
  state: string
}

export interface ApprovalRecord extends ApprovalSummary {
  operation?: string
  args: unknown
  argsHash: string
  preview?: string
  previewWarning?: string
  requester: { sub: string; user?: string; groups?: string[] }
  policy?: string
  decision?: string
  approverSub?: string
}

function csrfToken(): string {
  const match = document.cookie.match(/(?:^|; )approval_csrf=([^;]+)/)
  return match ? decodeURIComponent(match[1]) : ''
}

async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  headers.set('Accept', 'application/json')
  if (options.body) headers.set('Content-Type', 'application/json')
  const response = await fetch(path, { ...options, headers })
  if (!response.ok) {
    let message = response.statusText
    try {
      const body = await response.json()
      if (body.error) message = body.error
    } catch {
      // keep status text
    }
    throw new Error(message)
  }
  return response.json() as Promise<T>
}

export function useApprovalApi() {
  return {
    csrfToken,
    inbox: () => request<ApprovalSummary[]>('/api/v1/approvals'),
    detail: (id: string) => request<ApprovalRecord>(`/api/v1/approvals/${encodeURIComponent(id)}`),
    decide: (id: string, decision: 'approve' | 'reject') =>
      request(`/api/v1/approvals/${encodeURIComponent(id)}/decide`, {
        method: 'POST',
        headers: { 'X-CSRF-Token': csrfToken() },
        body: JSON.stringify({ decision }),
      }),
    logout: () =>
      fetch('/auth/logout', {
        method: 'POST',
        headers: { 'X-CSRF-Token': csrfToken() },
      }),
  }
}
