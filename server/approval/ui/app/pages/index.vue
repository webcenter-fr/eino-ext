<script setup lang="ts">
import type { ApprovalSummary } from '~/composables/useApprovalApi'

const api = useApprovalApi()
const items = ref<ApprovalSummary[]>([])
const error = ref('')
const loading = ref(true)

async function load() {
  loading.value = true
  error.value = ''
  try {
    items.value = await api.inbox()
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

async function logout() {
  await api.logout()
  window.location.href = '/'
}

onMounted(load)
</script>

<template>
  <div class="min-h-screen">
    <header class="flex items-center justify-between border-b border-slate-700 px-6 py-4">
      <h1 class="text-lg font-semibold">Approval inbox</h1>
      <div class="flex items-center gap-3">
        <UButton to="/auth/login" variant="ghost">Login</UButton>
        <UButton color="error" variant="soft" @click="logout">Logout</UButton>
      </div>
    </header>
    <main class="mx-auto max-w-4xl space-y-4 p-6">
      <UAlert v-if="error" color="error" :title="error" />
      <p v-if="loading" class="text-slate-400">Loading…</p>
      <p v-else-if="items.length === 0" class="text-slate-400">No pending approvals.</p>
      <UCard v-for="item in items" :key="item.approvalId">
        <div class="flex flex-wrap items-center gap-3">
          <strong>{{ item.toolName }}</strong>
          <UBadge variant="soft">{{ item.serverName }}</UBadge>
          <UBadge variant="soft">{{ item.instance || 'default' }}</UBadge>
        </div>
        <p class="mt-2 text-sm text-slate-400">
          Requested by {{ item.requester || item.requesterSub }} · expires {{ item.expiresAt }}
        </p>
        <UButton class="mt-3" :to="`/approvals/${encodeURIComponent(item.approvalId)}`">Review</UButton>
      </UCard>
    </main>
  </div>
</template>
