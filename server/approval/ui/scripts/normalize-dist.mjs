// Normalize non-deterministic build metadata so the committed dist is
// reproducible and the CI staleness check (git diff --exit-code) is meaningful.
import { readdir, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { fileURLToPath } from 'node:url'

const dist = fileURLToPath(new URL('../dist/', import.meta.url))

async function normalize(path) {
  const text = await readFile(path, 'utf8')
  const next = text
    .replace(/"prerenderedAt":\d+/g, '"prerenderedAt":0')
    .replace(/"timestamp":\d+/g, '"timestamp":0')
    // devalue payload: [{"prerenderedAt":0,"serverRendered":2},<timestamp>,false]
    .replace(/("serverRendered":2},)\d+(,false\])/g, (_, before, after) => `${before}0${after}`)
  if (next !== text) {
    await writeFile(path, next)
  }
}

async function walk(dir) {
  for (const entry of await readdir(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name)
    if (entry.isDirectory()) {
      await walk(path)
    } else if (entry.name.endsWith('.html') || entry.name.endsWith('.json')) {
      await normalize(path)
    }
  }
}

await walk(dist)
