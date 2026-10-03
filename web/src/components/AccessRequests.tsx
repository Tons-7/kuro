import { useQuery } from '@tanstack/react-query'
import { api } from '../lib/api'
import { useAccessDevices, useDecideDevice } from '../lib/queries'
import { buttonClass } from './ui'

/** On the host, over every page: a device is asking to be let in. */
export function AccessRequests() {
  const access = useQuery({ queryKey: ['access'], queryFn: () => api.get<{ host?: boolean }>('/api/access') })
  const devices = useAccessDevices(!!access.data?.host)
  const decide = useDecideDevice()

  const waiting = (devices.data?.devices ?? []).filter((d) => d.status === 'pending')
  if (!waiting.length) return null

  return (
    <div className="fixed right-4 bottom-4 z-[90] flex w-80 max-w-[calc(100vw-2rem)] flex-col gap-2" role="alert">
      {waiting.slice(0, 3).map((d) => (
        <div key={d.id} className="rounded-xl border border-white/10 bg-base-900 p-4 shadow-2xl">
          <p className="text-sm font-semibold text-white">A device wants to use kuro</p>
          <p className="mt-1 text-xs text-base-300">
            {d.name} <span className="text-base-500">· {d.addr}</span>
          </p>
          <div className="mt-3 flex gap-2">
            <button
              onClick={() => decide.mutate({ id: d.id, status: 'approved' })}
              disabled={decide.isPending}
              className={buttonClass('primary')}
            >
              Accept
            </button>
            <button
              onClick={() => decide.mutate({ id: d.id, status: 'denied' })}
              disabled={decide.isPending}
              className={buttonClass()}
            >
              Decline
            </button>
          </div>
        </div>
      ))}
      {waiting.length > 3 && (
        <p className="text-right text-xs text-base-400">+{waiting.length - 3} more in Settings</p>
      )}
    </div>
  )
}
