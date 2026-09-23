import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { MutationCache, QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { BrowserRouter } from 'react-router-dom'
import './index.css'
import { App } from './App'
import { toast, Toaster } from './components/Toaster'

const client = new QueryClient({
  // A failed action says so. Those that show the error next to themselves
  // opt out with meta.inline, so it is not said twice.
  mutationCache: new MutationCache({
    onError: (error, _vars, _ctx, mutation) => {
      if (mutation.meta?.inline) return
      toast(error instanceof Error ? error.message : 'That did not work')
    },
  }),
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
      <Toaster />
    </QueryClientProvider>
  </StrictMode>,
)
