<template>
  <div class="space-y-3">
    <!-- 站选择 + 发起授权 -->
    <div>
      <label class="input-label">{{ t('admin.accounts.zcode.site') }}</label>
      <div class="flex flex-wrap gap-2">
        <button
          v-for="site in sites"
          :key="site.id"
          type="button"
          class="rounded-lg border px-3 py-2 text-left text-sm transition-colors"
          :class="
            selectedSite === site.id
              ? 'border-primary-500 bg-primary-50 text-primary-700 dark:border-primary-500 dark:bg-primary-900/30 dark:text-primary-300'
              : 'border-gray-200 text-gray-700 hover:bg-gray-50 dark:border-dark-600 dark:text-gray-200 dark:hover:bg-dark-700'
          "
          :disabled="status === 'ready' || loading"
          @click="selectedSite = site.id"
        >
          <span class="font-medium">{{ site.name }}</span>
          <span class="ml-1 text-xs text-gray-500 dark:text-gray-400">{{ site.description }}</span>
        </button>
      </div>
    </div>

    <div v-if="!authUrl" class="flex items-center gap-2">
      <button type="button" class="btn btn-secondary" :disabled="loading" @click="startLogin">
        <Icon v-if="loading" name="refresh" size="sm" class="mr-1 animate-spin" />
        <Icon v-else name="link" size="sm" class="mr-1" />
        {{ t('admin.accounts.zcode.startLogin') }}
      </button>
      <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.startHint') }}</span>
    </div>

    <!-- 授权链接 + 轮询状态 -->
    <div v-if="authUrl" class="rounded-lg border border-gray-200 p-3 dark:border-dark-600">
      <div class="flex flex-wrap items-center gap-2">
        <a :href="authUrl" target="_blank" rel="noopener" class="btn btn-secondary text-xs">
          <Icon name="link" size="sm" class="mr-1" />
          {{ t('admin.accounts.zcode.openAuthorize') }}
        </a>
        <button type="button" class="btn btn-secondary text-xs" :disabled="loading" @click="pollNow">
          <Icon name="refresh" size="sm" :class="['mr-1', polling ? 'animate-spin' : '']" />
          {{ t('admin.accounts.zcode.refreshStatus') }}
        </button>
        <span class="text-sm" :class="statusClass">{{ statusText }}</span>
      </div>
      <div v-if="pollResult?.email || pollResult?.user_id" class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
        <div class="rounded-md bg-gray-50 p-2 text-xs dark:bg-dark-700">
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.account') }}</span>
          <p class="font-medium text-gray-900 dark:text-gray-100">
            {{ pollResult?.email || pollResult?.name || pollResult?.user_id }}
          </p>
        </div>
        <div class="rounded-md bg-gray-50 p-2 text-xs dark:bg-dark-700">
          <span class="text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.endpoint') }}</span>
          <p class="break-all font-medium text-gray-900 dark:text-gray-100">{{ ultraBaseUrl }}</p>
        </div>
      </div>
      <div
        v-if="pollResult?.warning"
        class="mt-2 rounded-md border border-amber-200 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300"
      >
        {{ t('admin.accounts.zcode.provisionWarning', { reason: pollResult.warning }) }}
      </div>
    </div>

    <div
      v-if="errorMessage"
      class="rounded-lg border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
    >
      {{ errorMessage }}
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import Icon from '@/components/icons/Icon.vue'
import zcodeAPI, { type ZCodePollResponse, type ZCodeSiteOption } from '@/api/admin/zcode'

interface Props {
  proxyId?: number | null
  autoStart?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  proxyId: null,
  autoStart: false
})

const { t } = useI18n()

const ultraBaseUrl = 'https://zcode.z.ai/api/v1/ultra/anthropic'
const pollIntervalMs = 3000

const sites = ref<ZCodeSiteOption[]>([
  { id: 'bigmodel', name: 'BigModel', description: '' },
  { id: 'zai', name: 'Z.AI', description: '' }
])
const selectedSite = ref('bigmodel')
const authUrl = ref('')
const state = ref('')
const pollResult = ref<ZCodePollResponse | null>(null)
const loading = ref(false)
const polling = ref(false)
const errorMessage = ref('')

let timer: ReturnType<typeof setInterval> | null = null

const status = computed(() => pollResult.value?.status ?? (authUrl.value ? 'pending' : 'idle'))

const statusText = computed(() => {
  switch (status.value) {
    case 'ready':
      return t('admin.accounts.zcode.statusReady')
    case 'declined':
      return t('admin.accounts.zcode.statusDeclined')
    case 'pending':
      return t('admin.accounts.zcode.statusPending')
    default:
      return ''
  }
})

const statusClass = computed(() =>
  status.value === 'ready'
    ? 'text-emerald-600 dark:text-emerald-400'
    : status.value === 'declined'
      ? 'text-red-600 dark:text-red-400'
      : 'text-gray-500 dark:text-gray-400'
)

function stopPolling() {
  if (timer) {
    clearInterval(timer)
    timer = null
  }
}

function reset() {
  stopPolling()
  authUrl.value = ''
  state.value = ''
  pollResult.value = null
  loading.value = false
  polling.value = false
  errorMessage.value = ''
}

async function loadSites() {
  try {
    const list = await zcodeAPI.getSites()
    if (list?.length) sites.value = list
  } catch {
    // 站点列表拉取失败沿用内置两项，不阻塞登录。
  }
}

async function startLogin() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await zcodeAPI.generateAuthUrl({
      site: selectedSite.value,
      proxy_id: props.proxyId ?? null
    })
    authUrl.value = result.auth_url
    state.value = result.state
    pollResult.value = null
    window.open(result.auth_url, '_blank', 'noopener')
    startPolling()
  } catch (err: any) {
    errorMessage.value = err?.response?.data?.message || err?.message || String(err)
  } finally {
    loading.value = false
  }
}

function startPolling() {
  stopPolling()
  timer = setInterval(() => {
    void pollNow()
  }, pollIntervalMs)
}

async function pollNow() {
  if (!state.value || polling.value) return
  if (status.value === 'ready' || status.value === 'declined') {
    stopPolling()
    return
  }
  polling.value = true
  try {
    const result = await zcodeAPI.poll(state.value)
    pollResult.value = result
    if (result.status === 'ready' || result.status === 'declined') {
      stopPolling()
    }
  } catch (err: any) {
    errorMessage.value = err?.response?.data?.message || err?.message || String(err)
  } finally {
    polling.value = false
  }
}

onMounted(() => {
  void loadSites()
  if (props.autoStart) void startLogin()
})

onBeforeUnmount(stopPolling)

defineExpose({
  state,
  status,
  pollResult,
  reset,
  startLogin,
  pollNow
})
</script>
