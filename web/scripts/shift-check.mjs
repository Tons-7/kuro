// Navigating between a short page and a tall one must not move the layout.
import { chromium } from 'playwright'
const b = await chromium.launch()
const p = await b.newPage({ viewport: { width: 1500, height: 900 } })

await p.goto('http://127.0.0.1:4321/', { waitUntil: 'load', timeout: 60000 })
await p.waitForTimeout(8000)

const probe = async (where) => {
  const m = await p.evaluate(() => {
    const header = document.querySelector('header > div')
    return {
      innerWidth: window.innerWidth,
      docWidth: document.documentElement.clientWidth,
      headerLeft: Math.round(header?.getBoundingClientRect().left ?? -1),
      scrollable: document.documentElement.scrollHeight > window.innerHeight,
    }
  })
  console.log(where.padEnd(12), JSON.stringify(m))
  return m
}

const a = await probe('home')
await p.getByRole('link', { name: 'Settings', exact: true }).count().catch(() => 0)
for (const [name, path] of [['/settings','/settings'],['/library','/library'],['/recent','/recent'],['/','/']]) {
  await p.goto('http://127.0.0.1:4321' + path, { waitUntil: 'load', timeout: 60000 })
  await p.waitForTimeout(5000)
  await probe(name)
}

await b.close()
