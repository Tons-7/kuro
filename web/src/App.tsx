import { lazy, Suspense } from 'react'
import { Route, Routes } from 'react-router-dom'
import { Layout } from './components/Layout'
import { Centered, LinkButton, Spinner } from './components/ui'
import { Home } from './pages/Home'

// The player pulls in hls.js and the subtitle renderer, which no other screen
// needs; keeping it out of the initial bundle is most of the payload.
const Watch = lazy(() => import('./pages/Watch').then((m) => ({ default: m.Watch })))
const Anime = lazy(() => import('./pages/Anime').then((m) => ({ default: m.Anime })))
const Browse = lazy(() => import('./pages/Browse').then((m) => ({ default: m.Browse })))
const Library = lazy(() => import('./pages/Library').then((m) => ({ default: m.Library })))
const History = lazy(() => import('./pages/History').then((m) => ({ default: m.History })))
const SchedulePage = lazy(() =>
  import('./pages/Schedule').then((m) => ({ default: m.SchedulePage })),
)
const RecentPage = lazy(() => import('./pages/Recent').then((m) => ({ default: m.RecentPage })))
const Settings = lazy(() => import('./pages/Settings').then((m) => ({ default: m.Settings })))
const Notifications = lazy(() =>
  import('./pages/Manage').then((m) => ({ default: m.Notifications })),
)
const Downloads = lazy(() => import('./pages/Manage').then((m) => ({ default: m.Downloads })))
const LocalFiles = lazy(() => import('./pages/Manage').then((m) => ({ default: m.LocalFiles })))
const SetupPage = lazy(() => import('./pages/Setup').then((m) => ({ default: m.SetupPage })))

function Loading() {
  return (
    <Centered>
      <Spinner />
    </Centered>
  )
}

export function App() {
  return (
    <Suspense fallback={<Loading />}>
      <Routes>
        <Route element={<Layout />}>
          <Route index element={<Home />} />
          {/* The player is full-bleed but keeps the nav: losing it on the one
              page you spend the most time on strands you there. */}
          <Route path="watch/:animeId/:episode" element={<Watch />} />
          <Route path="anime/:animeId" element={<Anime />} />
          <Route path="browse" element={<Browse />} />
          <Route path="schedule" element={<SchedulePage />} />
          <Route path="recent" element={<RecentPage />} />
          <Route path="library" element={<Library />} />
          <Route path="history" element={<History />} />
          <Route path="settings" element={<Settings />} />
          <Route path="notifications" element={<Notifications />} />
          <Route path="downloads" element={<Downloads />} />
          <Route path="local" element={<LocalFiles />} />
          <Route path="setup" element={<SetupPage />} />
          <Route path="*" element={<NotFound />} />
        </Route>
      </Routes>
    </Suspense>
  )
}

function NotFound() {
  return (
    <Centered>
      <div className="text-center">
        <p className="bg-gradient-to-b from-accent-400 to-accent-600/40 bg-clip-text text-7xl font-bold tracking-tight text-transparent">
          404
        </p>
        <p className="mt-2 text-base-200">That page does not exist.</p>
        <p className="mt-1 text-sm text-base-500">The link may be old, or the show was removed.</p>
        <div className="mt-5 flex justify-center gap-2">
          <LinkButton to="/" variant="primary">
            Go home
          </LinkButton>
          <LinkButton to="/browse">Browse anime</LinkButton>
        </div>
      </div>
    </Centered>
  )
}
