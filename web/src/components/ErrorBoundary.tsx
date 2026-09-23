import { Component, type ReactNode } from 'react'
import { cx } from '../lib/format'
import { buttonClass } from './ui'

// After a self-update the old page asks for chunks that no longer exist; any
// render error used to blank the whole app with nothing to click.
const STALE_CHUNK = /dynamically imported module|Importing a module script failed|error loading dynamically/i

export class ErrorBoundary extends Component<{ children: ReactNode }, { error: Error | null }> {
  state: { error: Error | null } = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children
    const updated = STALE_CHUNK.test(error.message)
    return (
      <div className="grid min-h-[50vh] place-items-center p-6 text-center">
        <div className="max-w-md">
          <p className="text-base font-medium text-white">
            {updated ? 'kuro was updated' : 'This page hit a problem'}
          </p>
          <p className="mt-1.5 text-sm text-base-400">
            {updated
              ? 'Reload to pick up the new version.'
              : `Reloading usually fixes it. (${error.message.slice(0, 160)})`}
          </p>
          <button
            onClick={() => window.location.reload()}
            className={cx(buttonClass('primary'), 'mt-4')}
          >
            Reload
          </button>
        </div>
      </div>
    )
  }
}
