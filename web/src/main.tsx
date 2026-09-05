import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import './index.css'
import { App } from './App'

const client = new QueryClient({
  defaultOptions: {
    queries: {
      // Everything upstream is rate limited or slow, so a re-render must not
      // become a re-fetch; window focus still refreshes what has gone stale.
      staleTime: 60_000,
      refetchOnWindowFocus: true,
      retry: (failures, error) => {
        // A rejected token or a bad request will fail identically forever.
        const status = (error as { status?: number })?.status ?? 0
        if (status >= 400 && status < 500) return false
        return failures < 2
      },
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={client}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
)
