import { useEffect, useState } from 'react'
import { UNPAIRED_EVENT } from '../lib/api'
import { cx } from '../lib/format'
import { buttonClass } from './ui'

/** Over everything once the server refuses this device's token. */
export function Unpaired() {
  const [unpaired, setUnpaired] = useState(false)
  useEffect(() => {
    const on = () => setUnpaired(true)
    window.addEventListener(UNPAIRED_EVENT, on)
    return () => window.removeEventListener(UNPAIRED_EVENT, on)
  }, [])
  if (!unpaired) return null

  return (
    <div className="fixed inset-0 z-[100] grid place-items-center bg-base-950/95 p-6 text-center backdrop-blur">
      <div className="max-w-sm">
        <p className="text-lg font-semibold text-white">This device needs pairing again</p>
        <p className="mt-2 text-sm text-base-300">
          The link it was using was signed out. On the computer running kuro, open Settings › Watch on
          your phone and scan the code again.
        </p>
        <button
          onClick={() => {
            try {
              localStorage.removeItem('kuro.token')
            } catch {
              // Nothing stored is fine.
            }
            window.location.reload()
          }}
          className={cx(buttonClass('primary'), 'mt-5')}
        >
          I've scanned it — reload
        </button>
      </div>
    </div>
  )
}
