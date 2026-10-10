import { useEffect, useState } from 'react'

/** The key pressed, lowercased, when it is a shortcut: not while typing, not with Ctrl, Alt or Cmd. */
export function plainKey(e: KeyboardEvent): string {
  if (e.ctrlKey || e.metaKey || e.altKey) return ''
  const target = e.target as HTMLElement | null
  if (target && (['INPUT', 'TEXTAREA', 'SELECT'].includes(target.tagName) || target.isContentEditable)) return ''
  return e.key.toLowerCase()
}

/** Where an overlay must be drawn to be seen: a browser shows nothing outside the fullscreen element. */
export function usePortalHome(): HTMLElement {
  const current = () => (document.fullscreenElement as HTMLElement | null) ?? document.body
  const [home, setHome] = useState(current)
  useEffect(() => {
    const onChange = () => setHome(current())
    document.addEventListener('fullscreenchange', onChange)
    return () => document.removeEventListener('fullscreenchange', onChange)
  }, [])
  return home
}

/** Asks the shortcuts list to open, from a menu that does not own it. */
export const SHOW_SHORTCUTS = 'kuro:shortcuts'

export interface Shortcut {
  keys: string[]
  does: string
}

export const SHORTCUTS: { where: string; items: Shortcut[] }[] = [
  {
    where: 'While watching',
    items: [
      { keys: ['Space', 'K'], does: 'Play or pause' },
      { keys: ['←', '→'], does: 'Back or forward 5 seconds' },
      { keys: ['J', 'L'], does: 'Back or forward 10 seconds' },
      { keys: ['S'], does: 'Skip the opening or ending, while the button shows' },
      { keys: ['↑', '↓'], does: 'Volume up or down' },
      { keys: ['M'], does: 'Mute' },
      { keys: ['F'], does: 'Fullscreen' },
      { keys: ['Z', 'X'], does: 'Subtitles earlier or later' },
    ],
  },
  {
    where: 'Anywhere',
    items: [
      { keys: ['/'], does: 'Search' },
      { keys: ['D'], does: 'Show or hide downloads' },
      { keys: ['?'], does: 'Show or hide this list' },
      { keys: ['Esc'], does: 'Close a panel or dialog' },
    ],
  },
]
