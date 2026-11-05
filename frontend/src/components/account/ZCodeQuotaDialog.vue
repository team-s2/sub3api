<template>
  <BaseDialog :show="show" :title="t('admin.accounts.zcode.quota.title')" width="wide" @close="handleClose">
    <div class="space-y-4">
      <!-- 账号 + 开关 -->
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{{ account?.name || '-' }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.zcode.quota.usageUpdatedAt', { time: usageUpdatedAtText }) }}
          </p>
        </div>
        <div class="flex flex-wrap items-center gap-4">
          <label class="flex cursor-pointer items-center gap-2 text-xs text-gray-700 dark:text-gray-200">
            <input v-model="autoClaim" type="checkbox" class="rounded" @change="saveSettings" />
            {{ t('admin.accounts.zcode.resetCards.autoClaim') }}
          </label>
          <label class="flex cursor-pointer items-center gap-2 text-xs text-gray-700 dark:text-gray-200">
            <input v-model="peakPause" type="checkbox" class="rounded" @change="saveSettings" />
            {{ t('admin.accounts.zcode.resetCards.peakPause') }}
          </label>
          <button type="button" class="btn btn-secondary text-xs" :disabled="loading" @click="reload">
            <Icon name="refresh" size="sm" :class="['mr-1', loading ? 'animate-spin' : '']" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </div>

      <!-- 两个额度窗口，各自带上对应的重置卡 -->
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div
          v-for="window in quotaWindows"
          :key="window.key"
          class="flex flex-col rounded-xl border border-gray-200 p-4 dark:border-dark-600"
        >
          <div class="flex items-start justify-between gap-2">
            <div class="min-w-0">
              <p class="text-sm font-semibold text-gray-900 dark:text-gray-100">{{ window.title }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">{{ window.description }}</p>
            </div>
            <span class="shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium" :class="window.badgeClass">
              {{ window.percentLabel }}
            </span>
          </div>

          <div class="mt-3 h-2 w-full overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700">
            <div class="h-full rounded-full transition-all" :class="window.barClass" :style="{ width: window.percent + '%' }" />
          </div>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.zcode.quota.resetsAt', { time: window.resetsAtLabel }) }}
          </p>

          <!-- 重置卡：剩余张数 / 最近到期 / 重置按钮 -->
          <div class="mt-3 flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-dark-700">
            <div class="min-w-0">
              <p class="text-xs font-medium text-gray-700 dark:text-gray-200">
                {{ t('admin.accounts.zcode.resetCards.remainingCount', { count: window.cardCount }) }}
              </p>
              <p class="mt-0.5 text-[11px] text-gray-500 dark:text-gray-400">
                {{ window.earliestLabel }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-secondary text-xs"
              :disabled="using === window.resetType || window.cardCount === 0"
              :data-testid="`zcode-use-reset-${window.key}`"
              @click="useCard(window.resetType)"
            >
              <Icon v-if="using === window.resetType" name="refresh" size="sm" class="mr-1 animate-spin" />
              <Icon v-else name="bolt" size="sm" class="mr-1" />
              {{ t('admin.accounts.zcode.resetCards.use') }}
            </button>
          </div>
        </div>
      </div>

      <div
        v-if="actionError"
        class="rounded-lg border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
      >
        {{ actionError }}
      </div>

      <div v-if="loading && !cards.length" class="py-6 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>
      <p v-else-if="!cards.length" class="text-xs text-gray-500 dark:text-gray-400">
        {{ t('admin.accounts.zcode.resetCards.emptyHint') }}
      </p>

      <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.useHint') }}</p>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import zcodeAPI, { type ZCodeResetCard } from '@/api/admin/zcode'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { Account } from '@/types'

interface Props {
  show: boolean
  account?: Account | null
}

const props = withDefaults(defineProps<Props>(), { account: null })
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const using = ref('')
const cards = ref<ZCodeResetCard[]>([])
const detail = ref<Account | null>(null)
const autoClaim = ref(true)
const peakPause = ref(false)
const actionError = ref('')

const currentAccount = computed(() => detail.value ?? props.account)
const accountExtra = computed(() => (currentAccount.value?.extra ?? {}) as Record<string, unknown>)

function formatDateTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  return d.toLocaleString()
}

function clampPercent(value: unknown): number {
  const num = typeof value === 'number' ? value : Number(value)
  if (!Number.isFinite(num)) return 0
  return Math.max(0, Math.min(100, Math.round(num)))
}

const usageUpdatedAtText = computed(() => {
  const value = accountExtra.value.zhipu_usage_updated_at
  return typeof value === 'string' ? formatDateTime(value) : '-'
})

function cardsOfType(type: string): ZCodeResetCard[] {
  return cards.value.filter((card) => card.type === type)
}

function earliestExpiryLabel(list: ZCodeResetCard[]): string {
  const stamps = list
    .map((card) => new Date(card.expires_at).getTime())
    .filter((value) => Number.isFinite(value) && value > 0)
  if (!stamps.length) return t('admin.accounts.zcode.resetCards.noCards')
  return t('admin.accounts.zcode.resetCards.nearestExpiry', {
    time: formatDateTime(new Date(Math.min(...stamps)).toISOString())
  })
}

const quotaWindows = computed(() => {
  const build = (opts: {
    key: 'five_hour' | 'weekly'
    resetType: string
    title: string
    description: string
    usedKey: string
    resetKey: string
  }) => {
    const usedValue = accountExtra.value[opts.usedKey]
    const hasUsage = typeof usedValue === 'number' || typeof usedValue === 'string'
    const percent = hasUsage ? clampPercent(usedValue) : 0
    const resetValue = accountExtra.value[opts.resetKey]
    const groupCards = cardsOfType(opts.resetType)
    return {
      key: opts.key,
      resetType: opts.resetType,
      title: opts.title,
      description: opts.description,
      percent,
      percentLabel: hasUsage ? `${percent}%` : '-',
      resetsAtLabel: typeof resetValue === 'string' ? formatDateTime(resetValue) : '-',
      cardCount: groupCards.length,
      earliestLabel: earliestExpiryLabel(groupCards),
      barClass: percent >= 90 ? 'bg-red-500' : percent >= 70 ? 'bg-amber-500' : 'bg-primary-500',
      badgeClass:
        percent >= 90
          ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
          : percent >= 70
            ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
            : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    }
  }

  return [
    build({
      key: 'five_hour',
      resetType: 'FIVE_HOUR',
      title: t('admin.accounts.zcode.quota.fiveHour'),
      description: t('admin.accounts.zcode.quota.fiveHourDesc'),
      usedKey: 'zhipu_5h_used_percent',
      resetKey: 'zhipu_5h_reset_at'
    }),
    build({
      key: 'weekly',
      resetType: 'WEEK',
      title: t('admin.accounts.zcode.quota.weekly'),
      description: t('admin.accounts.zcode.quota.weeklyDesc'),
      usedKey: 'zhipu_weekly_used_percent',
      resetKey: 'zhipu_weekly_reset_at'
    })
  ]
})

function syncSettingsFromAccount() {
  autoClaim.value = accountExtra.value.zcode_auto_claim !== false
  peakPause.value = accountExtra.value.zcode_peak_pause === true
}

async function loadAccount() {
  if (!props.account) return
  try {
    detail.value = await adminAPI.accounts.getById(props.account.id)
  } catch (error: any) {
    console.warn('Failed to load zcode account detail:', error?.message || error)
  }
}

async function loadCards(force = false) {
  if (!props.account) return
  try {
    const inventory = await zcodeAPI.getResetCards(props.account.id, force)
    cards.value = inventory?.cards ?? []
  } catch (error: any) {
    appStore.showError(error?.response?.data?.message || error?.message || String(error))
  }
}

async function reload() {
  if (!props.account) return
  loading.value = true
  actionError.value = ''
  try {
    await Promise.all([loadAccount(), loadCards(true)])
    syncSettingsFromAccount()
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  if (!props.account) return
  try {
    await zcodeAPI.updateSettings(props.account.id, {
      peak_pause: peakPause.value,
      auto_claim: autoClaim.value
    })
    appStore.showSuccess(t('admin.accounts.zcode.resetCards.settingsSaved'))
  } catch (error: any) {
    appStore.showError(error?.response?.data?.message || error?.message || String(error))
  }
}

async function useCard(resetType: string) {
  if (!props.account) return
  using.value = resetType
  actionError.value = ''
  try {
    const inventory = await zcodeAPI.useResetCard(props.account.id, resetType)
    cards.value = inventory?.cards ?? []
    appStore.showSuccess(t('admin.accounts.zcode.resetCards.used'))
    void loadAccount()
  } catch (error: any) {
    actionError.value = error?.response?.data?.message || error?.message || String(error)
  } finally {
    using.value = ''
  }
}

function handleClose() {
  emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    cards.value = []
    detail.value = null
    actionError.value = ''
    syncSettingsFromAccount()
    void reload()
  }
)
</script>
