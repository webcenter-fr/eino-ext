export default defineNuxtConfig({
  ssr: false,
  // Deterministic build id so the committed dist is reproducible and the CI
  // staleness check (git diff --exit-code) is meaningful.
  buildId: 'approval-ui',
  compatibilityDate: '2025-01-01',
  devtools: { enabled: false },
  modules: ['@nuxt/ui'],
  css: ['~/assets/css/main.css'],
  app: {
    baseURL: '/',
    head: {
      title: 'Approval inbox',
      meta: [{ name: 'viewport', content: 'width=device-width, initial-scale=1' }],
    },
  },
  nitro: {
    output: {
      publicDir: 'dist',
    },
  },
})
