<script setup lang="ts">
const props = defineProps<{
  before: unknown
  after: string
}>()

function pretty(value: unknown): string {
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  return JSON.stringify(value, null, 2)
}

function toYaml(value: unknown, indent = 0): string {
  const pad = ' '.repeat(indent)
  if (value === null || value === undefined) return `${pad}null`
  if (Array.isArray(value)) {
    return value.map((item) => `${pad}- ${toYaml(item, indent + 2).trimStart()}`).join('\n')
  }
  if (typeof value === 'object') {
    return Object.entries(value as Record<string, unknown>)
      .map(([key, item]) => {
        if (item !== null && typeof item === 'object') {
          return `${pad}${key}:\n${toYaml(item, indent + 2)}`
        }
        return `${pad}${key}: ${String(item)}`
      })
      .join('\n')
  }
  return `${pad}${String(value)}`
}

interface DiffLine {
  kind: 'same' | 'add' | 'del'
  text: string
}

function lineDiff(before: string, after: string): DiffLine[] {
  const a = before.split('\n')
  const b = after.split('\n')
  const dp = Array.from({ length: a.length + 1 }, () => new Array<number>(b.length + 1).fill(0))
  for (let i = a.length - 1; i >= 0; i--) {
    for (let j = b.length - 1; j >= 0; j--) {
      dp[i][j] = a[i] === b[j] ? dp[i + 1][j + 1] + 1 : Math.max(dp[i + 1][j], dp[i][j + 1])
    }
  }
  const out: DiffLine[] = []
  let i = 0
  let j = 0
  while (i < a.length && j < b.length) {
    if (a[i] === b[j]) {
      out.push({ kind: 'same', text: a[i] })
      i++
      j++
    } else if (dp[i + 1][j] >= dp[i][j + 1]) {
      out.push({ kind: 'del', text: a[i++] })
    } else {
      out.push({ kind: 'add', text: b[j++] })
    }
  }
  while (i < a.length) out.push({ kind: 'del', text: a[i++] })
  while (j < b.length) out.push({ kind: 'add', text: b[j++] })
  return out
}

const jsonBefore = computed(() => pretty(props.before))
const jsonAfter = computed(() => pretty(props.after))
const yamlBefore = computed(() => {
  try {
    return toYaml(JSON.parse(jsonBefore.value))
  } catch {
    return jsonBefore.value
  }
})
const yamlAfter = computed(() => {
  try {
    return toYaml(JSON.parse(jsonAfter.value))
  } catch {
    return jsonAfter.value
  }
})
const diff = computed(() => lineDiff(jsonBefore.value, jsonAfter.value))
</script>

<template>
  <div class="space-y-3">
    <details open>
      <summary class="cursor-pointer font-semibold">Arguments (JSON)</summary>
      <pre class="mt-2 max-h-96 overflow-auto rounded bg-slate-950 p-3 text-xs">{{ jsonBefore }}</pre>
    </details>
    <details>
      <summary class="cursor-pointer font-semibold">Arguments (YAML)</summary>
      <pre class="mt-2 max-h-96 overflow-auto rounded bg-slate-950 p-3 text-xs">{{ yamlBefore }}</pre>
    </details>
    <details v-if="after">
      <summary class="cursor-pointer font-semibold">Dry-run preview</summary>
      <pre class="mt-2 max-h-96 overflow-auto rounded bg-slate-950 p-3 text-xs">{{ jsonAfter }}</pre>
    </details>
    <details v-if="after">
      <summary class="cursor-pointer font-semibold">Diff (arguments → preview)</summary>
      <pre class="mt-2 max-h-96 overflow-auto rounded bg-slate-950 p-3 text-xs"><div
        v-for="(line, index) in diff"
        :key="index"
        :class="line.kind === 'add' ? 'text-green-300' : line.kind === 'del' ? 'text-red-300' : ''"
      >{{ line.kind === 'add' ? '+' : line.kind === 'del' ? '-' : ' ' }} {{ line.text }}</div></pre>
    </details>
    <details v-if="after">
      <summary class="cursor-pointer font-semibold">Preview (YAML)</summary>
      <pre class="mt-2 max-h-96 overflow-auto rounded bg-slate-950 p-3 text-xs">{{ yamlAfter }}</pre>
    </details>
  </div>
</template>
