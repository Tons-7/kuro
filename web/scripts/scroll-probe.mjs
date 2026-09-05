// Where does the schedule actually land when it opens?
import { chromium } from 'playwright'

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1500, height: 950 } })
page.on('pageerror', (e) => console.log('PAGE ERROR:', e.message.slice(0, 200)))

await page.goto('http://127.0.0.1:4321/schedule', { waitUntil: 'load', timeout: 60000 })
await page.waitForTimeout(9000)

console.log(
  JSON.stringify(
    await page.evaluate(() => {
      const items = [...document.querySelectorAll('ul > li')]
      const next = items.findIndex((li) => li.innerText.includes('Next'))
      return {
        pageScrollY: Math.round(scrollY),
        pageScrollMax: Math.round(document.body.scrollHeight - innerHeight),
        rows: items.length,
        nextIndex: next,
        nextText: next >= 0 ? items[next].innerText.replace(/\n/g, ' | ').slice(0, 90) : null,
        nextTopInViewport: next >= 0 ? Math.round(items[next].getBoundingClientRect().top) : null,
      }
    }),
    null,
    1,
  ),
)

// The sidebar widget on the home page uses its own scroller.
await page.goto('http://127.0.0.1:4321/', { waitUntil: 'load', timeout: 60000 })
await page.waitForTimeout(9000)
console.log(
  JSON.stringify(
    await page.evaluate(() => {
      const list = [...document.querySelectorAll('ul')].find((u) =>
        u.className.includes('overflow-y-auto'),
      )
      if (!list) return { widget: 'not found' }
      const items = [...list.children]
      const next = items.findIndex((li) => li.innerText.includes('Next'))
      return {
        widget: true,
        scrollTop: Math.round(list.scrollTop),
        scrollMax: Math.round(list.scrollHeight - list.clientHeight),
        rows: items.length,
        nextIndex: next,
        nextTopInList:
          next >= 0
            ? Math.round(
                items[next].getBoundingClientRect().top - list.getBoundingClientRect().top,
              )
            : null,
        listHeight: Math.round(list.clientHeight),
        offsetParent: list.offsetParent?.className?.slice(0, 60) ?? null,
        nextOffsetTop: next >= 0 ? items[next].offsetTop : null,
      }
    }),
    null,
    1,
  ),
)

await browser.close()
