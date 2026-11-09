<script setup lang="ts">
import type { ApprovalRecord } from '~/composables/useApprovalApi'

const route = useRoute()
const api = useApprovalApi()
const record = ref<ApprovalRecord | null>(null)
const error = ref('')
const loading = ref(true)

async function load() {
  loading.value = true
  error.value = ''
  try {
    record.value = await api.detail(String(route.params.id))
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function decide(decision: 'approve' | 'reject') {
  if (!record.value) return
  try {
    await api.decide(record.value.approvalId, decision)
    window.location.href = '/'
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
}

onMounted(load)
</script>

<template>
  <div class="min-h-screen">
    <header class="flex items-center justify-between border-b border-slate-700 px-6 py-4">
      <NuxtLink to="/" class="text-lg font-semibold">Approval inbox</NuxtLink>
      <UButton to="/auth/login" variant="ghost">Login</UButton>
    </header>
    <main class="mx-auto max-w-4xl space-y-4 p-6">
      <UAlert v-if="error" color="error" :title="error" />
      <p v-if="loading" class="text-slate-400">Loading…</p>
      <template v-else-if="record">
        <div>
          <h1 class="text-2xl font-semibold">{{ record.toolName }}</h1>
          <p class="mt-1 text-sm text-slate-400">
            {{ record.serverName }} · {{ record.instance || 'default' }} · requested by
            {{ record.requester.user || record.requester.sub }}
          </p>
          <p class="text-sm text-slate-400">State: {{ record.state }} · expires {{ record.expiresAt }}</p>
        </div>
        <UAlert v-if="record.previewWarning" color="warning" title="Preview warning" :description="record.previewWarning" />
        <UCard>
          <DiffViewer :before="record.args" :after="record.preview || ''" />
        </UCard>
        <div v-if="record.state === 'pending'" class="flex gap-3">
          <UButton color="success" @click="decide('approve')">Approve</UButton>
          <UButton color="error" @click="decide('reject')">Reject</UButton>
        </div>
        <p v-else class="text-slate-400">Decision: {{ record.decision || record.state }}</p>
      </template>
    </main>
  </div>
</template>
