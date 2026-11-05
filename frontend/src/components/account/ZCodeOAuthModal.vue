<template>
  <BaseDialog :show="show" :title="t('admin.accounts.zcode.title')" width="wide" @close="handleClose">
    <div class="space-y-4">
      <!-- 说明 -->
      <div
        class="rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-800 dark:border-blue-700 dark:bg-blue-900/30 dark:text-blue-200"
      >
        {{ t('admin.accounts.zcode.intro') }}
      </div>

      <!-- 1. 选择登录入口 -->
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

      <!-- 2. 发起授权 -->
      <div v-if="!authUrl" class="flex items-center gap-2">
        <button type="button" class="btn btn-primary" :disabled="loading" @click="startLogin">
          <Icon v-if="loading" name="refresh" size="sm" class="mr-1 animate-spin" />
          {{ t('admin.accounts.zcode.startLogin') }}
        </button>
        <span class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.startHint') }}</span>
      </div>

      <!-- 3. 授权链接 + 轮询状态 -->
      <div v-if="authUrl" class="rounded-lg border border-gray-200 p-4 dark:border-dark-600">
        <div class="flex flex-wrap items-center gap-2">
          <a :href="authUrl" target="_blank" rel="noopener" class="btn btn-primary">
            <Icon name="link" size="sm" class="mr-1" />
            {{ t('admin.accounts.zcode.openAuthorize') }}
          </a>
          <button type="button" class="btn btn-secondary" :disabled="loading" @click="pollNow">
            <Icon name="refresh" size="sm" :class="['mr-1', polling ? 'animate-spin' : '']" />
            {{ t('admin.accounts.zcode.refreshStatus') }}
          </button>
          <span class="text-sm" :class="statusClass">{{ statusText }}</span>
        </div>
        <p class="mt-2 break-all text-xs text-gray-500 dark:text-gray-400">{{ authUrl }}</p>
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
          class="mt-3 rounded-md border border-amber-200 bg-amber-50 p-2 text-xs text-amber-800 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300"
        >
          {{ t('admin.accounts.zcode.provisionWarning', { reason: pollResult.warning }) }}
        </div>
      </div>

      <!-- 4. 创建账号 -->
      <div v-if="status === 'ready'" class="space-y-3 border-t border-gray-200 pt-4 dark:border-dark-600">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div>
            <label class="input-label">{{ t('admin.accounts.zcode.accountName') }}</label>
            <input v-model="accountName" type="text" class="input" :placeholder="defaultAccountName" />
          </div>
          <div>
            <label class="input-label">{{ t('admin.accounts.zcode.concurrency') }}</label>
            <input v-model.number="concurrency" type="number" min="1" class="input" />
          </div>
        </div>

        <div>
          <label class="input-label">{{ t('admin.accounts.zcode.apiKey') }}</label>
          <input
            v-model="apiKeyOverride"
            type="text"
            class="input font-mono text-xs"
            :placeholder="pollResult?.masked_api_key || t('admin.accounts.zcode.apiKeyPlaceholder')"
          />
          <p class="input-hint">
            {{
              pollResult?.has_api_key
                ? t('admin.accounts.zcode.apiKeyAutoHint')
                : t('admin.accounts.zcode.apiKeyManualHint')
            }}
          </p>
        </div>

        <div v-if="proxies?.length">
          <label class="input-label">{{ t('admin.accounts.zcode.proxy') }}</label>
          <select v-model="proxyId" class="input">
            <option :value="null">{{ t('admin.accounts.zcode.proxyNone') }}</option>
            <option v-for="proxy in proxies" :key="proxy.id" :value="proxy.id">
              {{ proxy.name }} ({{ proxy.host }}:{{ proxy.port }})
            </option>
          </select>
        </div>

        <div v-if="groups?.length">
          <label class="input-label">{{ t('admin.accounts.zcode.groups') }}</label>
          <div class="max-h-32 space-y-1 overflow-y-auto rounded-lg border border-gray-200 p-2 dark:border-dark-600">
            <label
              v-for="group in groups"
              :key="group.id"
              class="flex cursor-pointer items-center gap-2 text-sm text-gray-700 dark:text-gray-200"
            >
              <input v-model="selectedGroupIds" type="checkbox" :value="group.id" class="rounded" />
              <span>{{ group.name }}</span>
            </label>
          </div>
        </div>
      </div>

      <!-- 错误 -->
      <div
        v-if="errorMessage"
        class="rounded-lg border border-red-200 bg-red-50 p-3 text-sm text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
      >
        {{ errorMessage }}
      </div>
    </div>

    <template #footer>
      <div class="flex justify-end gap-2">
        <button type="button" class="btn btn-secondary" @click="handleClose">
          {{ t('admin.accounts.zcode.cancel') }}
        </button>
        <button
          type="button"
          class="btn btn-primary"
          :disabled="status !== 'ready' || creating"
          @click="createAccount"
        >
          <Icon v-if="creating" name="refresh" size="sm" class="mr-1 animate-spin" />
          {{ t('admin.accounts.zcode.create') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import zcodeAPI, { type ZCodePollResponse, type ZCodeSiteOption } from '@/api/admin/zcode'
import type { Group, Proxy } from '@/types'

interface Props {
  show: boolean
  proxies?: Proxy[]
  groups?: Group[]
}

const props = withDefaults(defineProps<Props>(), {
  proxies: () => [],
  groups: () => []
})

const emit = defineEmits<{
  close: []
  created: []
}>()

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
const creating = ref(false)
const errorMessage = ref('')

const accountName = ref('')
const concurrency = ref(1)
const apiKeyOverride = ref('')
const proxyId = ref<number | null>(null)
const selectedGroupIds = ref<number[]>([])

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

const defaultAccountName = computed(() => {
  const identity = pollResult.value?.email || pollResult.value?.name || pollResult.value?.user_id
  return identity ? `ZCode ${identity}` : 'ZCode OAuth'
})

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
  errorMessage.value = ''
  loading.value = false
  polling.value = false
  creating.value = false
  accountName.value = ''
  concurrency.value = 1
  apiKeyOverride.value = ''
  proxyId.value = null
  selectedGroupIds.value = []
}

async function loadSites() {
  try {
    const list = await zcodeAPI.getSites()
    if (list?.length) sites.value = list
  } catch {
    // 站点列表拉取失败时沿用内置两项，不阻塞登录。
  }
}

async function startLogin() {
  loading.value = true
  errorMessage.value = ''
  try {
    const result = await zcodeAPI.generateAuthUrl({
      site: selectedSite.value,
      proxy_id: proxyId.value
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
      if (result.status === 'ready' && !accountName.value) {
        accountName.value = defaultAccountName.value
      }
    }
  } catch (err: any) {
    errorMessage.value = err?.response?.data?.message || err?.message || String(err)
  } finally {
    polling.value = false
  }
}

async function createAccount() {
  if (!state.value) return
  creating.value = true
  errorMessage.value = ''
  try {
    await zcodeAPI.createAccount({
      state: state.value,
      name: accountName.value.trim() || undefined,
      api_key: apiKeyOverride.value.trim() || undefined,
      proxy_id: proxyId.value,
      concurrency: concurrency.value > 0 ? concurrency.value : 1,
      group_ids: selectedGroupIds.value.length ? selectedGroupIds.value : undefined
    })
    emit('created')
    handleClose()
  } catch (err: any) {
    errorMessage.value = err?.response?.data?.message || err?.message || String(err)
  } finally {
    creating.value = false
  }
}

function handleClose() {
  reset()
  emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (visible) {
      reset()
      void loadSites()
    } else {
      stopPolling()
    }
  }
)

onBeforeUnmount(stopPolling)
</script>
