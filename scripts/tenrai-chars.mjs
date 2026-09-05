const j = await (await fetch('https://api.tenrai.org/v1/anime/52991/characters')).json()
const d = j.data ?? []
console.log('Frieren (MAL 52991) characters:', d.length)
console.log('roles:', [...new Set(d.map(c => c.role))].join(', '))
const top = [...d].sort((a, b) => b.favorites - a.favorites)[0]
console.log('\ntop entry:\n' + JSON.stringify(top, null, 1).slice(0, 1000))
