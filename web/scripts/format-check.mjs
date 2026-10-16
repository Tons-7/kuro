// The text helpers in src/lib/format.ts, run straight by Node: node scripts/format-check.mjs
const { relativeTime } = await import(new URL('../src/lib/format.ts', import.meta.url))

let failures = 0
const check = (got, want, label) => {
  if (got === want) return console.log(`ok   ${label}`)
  failures++
  console.log(`FAIL ${label}: got ${JSON.stringify(got)}, want ${JSON.stringify(want)}`)
}

const now = Date.now() / 1000
check(relativeTime(now + 30), 'in 1m', 'under a minute reads as a minute')
check(relativeTime(now + 59.6 * 60), 'in 1h', '59.6 minutes rounds to an hour, not "60m"')
check(relativeTime(now + 2 * 3600 + 59.7 * 60), 'in 3h', 'never "2h 60m"')
check(relativeTime(now - 90 * 60), '1h 30m ago', 'hours and minutes in the past')
check(relativeTime(now + 3 * 86400), 'in 3d', 'days')

console.log(failures ? `\n${failures} FAILED` : '\nall passed')
process.exit(failures ? 1 : 0)
