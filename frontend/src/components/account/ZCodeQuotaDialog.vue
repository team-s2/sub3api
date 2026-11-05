<template>
  <BaseDialog :show="show" :title="t('admin.accounts.zcode.quota.title')" width="wide" @close="handleClose">
    <div class="space-y-4">
      <!-- 账号 + 开关 -->
      <div class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-gray-200 p-3 dark:border-dark-600">
        <div class="min-w-0">
          <p class="truncate text-sm font-medium text-gray-900 dark:text-gray-100">{{ account?.name || '-' }}</p>
          <p class="text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.zcode.resetCards.observedAt', { time: observedAtText }) }}
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
          <button type="button" class="btn btn-secondary text-xs" :disabled="loading || usageLoading" @click="reload">
            <Icon name="refresh" size="sm" :class="['mr-1', loading || usageLoading ? 'animate-spin' : '']" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </div>

      <!-- 额度窗口 -->
      <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <div
          v-for="window in quotaWindows"
          :key="window.key"
          class="rounded-xl border border-gray-200 p-4 dark:border-dark-600"
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
            <div
              class="h-full rounded-full transition-all"
              :class="window.barClass"
              :style="{ width: window.percent + '%' }"
            />
          </div>
          <div class="mt-2 flex items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400">
            <span>{{ window.usedLabel }}</span>
            <span>{{ t('admin.accounts.zcode.quota.resetsAt', { time: window.resetsAtLabel }) }}</span>
          </div>
        </div>
      </div>

      <!-- 重置卡 -->
      <div class="rounded-xl border border-gray-200 dark:border-dark-600">
        <div class="flex flex-wrap items-center justify-between gap-2 border-b border-gray-100 px-4 py-3 dark:border-dark-700">
          <div>
            <p class="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {{ t('admin.accounts.zcode.resetCards.cardsTitle') }}
            </p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('admin.accounts.zcode.resetCards.cardsHint') }}
            </p>
          </div>
        </div>

        <div v-if="loading && !cards.length" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">
          {{ t('common.loading') }}
        </div>
        <div v-else-if="!cards.length" class="px-4 py-8 text-center">
          <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.empty') }}</p>
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ t('admin.accounts.zcode.resetCards.emptyHint') }}</p>
        </div>
        <div v-else class="divide-y divide-gray-100 dark:divide-dark-700">
          <div
            v-for="group in cardGroups"
            :key="group.type"
            class="flex flex-wrap items-center justify-between gap-3 px-4 py-3"
          >
            <div class="min-w-0">
              <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ group.title }}</p>
              <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                {{
                  t('admin.accounts.zcode.resetCards.availableCount', {
                    count: group.cards.length,
                    time: group.earliestLabel
                  })
                }}
              </p>
            </div>
            <button
              type="button"
              class="btn btn-secondary text-xs"
              :disabled="using === group.type || group.cards.length === 0"
              @click="useCard(group.type)"
            >
              <Icon v-if="using === group.type" name="refresh" size="sm" class="mr-1 animate-spin" />
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

      <!-- 单卡清单（折叠展示全部卡券过期时间） -->
      <details v-if="cards.length" class="rounded-xl border border-gray-200 px-4 py-3 dark:border-dark-600">
        <summary class="cursor-pointer text-xs font-medium text-gray-600 dark:text-gray-300">
          {{ t('admin.accounts.zcode.resetCards.cardList') }}
        </summary>
        <ul class="mt-2 space-y-1">
          <li
            v-for="card in cards"
            :key="card.type + card.expires_at"
            class="flex items-center justify-between gap-2 text-xs text-gray-500 dark:text-gray-400"
          >
            <span>{{ typeLabel(card.type) }}</span>
            <span>{{ formatDateTime(card.expires_at) }} · {{ remainingText(card) }}</span>
          </li>
        </ul>
      </details>

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
import type { Account, AccountUsageInfo } from '@/types'

interface Props {
  show: boolean
  account?: Account | null
}

const props = withDefaults(defineProps<Props>(), { account: null })
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const usageLoading = ref(false)
const using = ref('')
const cards = ref<ZCodeResetCard[]>([])
const observedAt = ref<string>('')
const usage = ref<AccountUsageInfo | null>(null)
const autoClaim = ref(true)
const peakPause = ref(false)
const actionError = ref('')

const observedAtText = computed(() => (observedAt.value ? formatDateTime(observedAt.value) : '-'))
const accountExtra = computed(() => (props.account?.extra ?? {}) as Record<string, unknown>)

function formatDateTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  return d.toLocaleString()
}

function clampPercent(value: number | undefined | null): number {
  if (!Number.isFinite(value ?? NaN)) return 0
  return Math.max(0, Math.min(100, Math.round(value as number)))
}

function usageWindowTitle(key: 'five_hour' | 'seven_day'): string {
  return key === 'five_hour'
    ? t('admin.accounts.zcode.quota.fiveHour')
    : t('admin.accounts.zcode.quota.weekly')
}

function usageWindowDesc(key: 'five_hour' | 'seven_day'): string {
  return key === 'five_hour'
    ? t('admin.accounts.zcode.quota.fiveHourDesc')
    : t('admin.accounts.zcode.quota.weeklyDesc')
}

const quotaWindows = computed(() => {
  const build = (key: 'five_hour' | 'seven_day') => {
    const window = usage.value?.[key] ?? null
    const percent = clampPercent(window?.utilization)
    return {
      key,
      title: usageWindowTitle(key),
      description: usageWindowDesc(key),
      percent,
      percentLabel: window ? `${percent}%` : '-',
      usedLabel: window
        ? t('admin.accounts.zcode.quota.used')
        : t('admin.accounts.zcode.quota.noData'),
      resetsAtLabel: window?.resets_at ? formatDateTime(window.resets_at) : '-',
      barClass:
        percent >= 90
          ? 'bg-red-500'
          : percent >= 70
            ? 'bg-amber-500'
            : 'bg-primary-500',
      badgeClass:
        percent >= 90
          ? 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
          : percent >= 70
            ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
            : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    }
  }
  return [build('five_hour'), build('seven_day')]
})

function typeLabel(type: string): string {
  return type === '5h'
    ? t('admin.accounts.zcode.resetCards.type5h')
    : t('admin.accounts.zcode.resetCards.typeWeek')
}

function earliestExpiry(list: ZCodeResetCard[]): number | null {
  const stamps = list
    .map((card) => new Date(card.expires_at).getTime())
    .filter((value) => Number.isFinite(value) && value > 0)
  if (!stamps.length) return null
  return Math.min(...stamps)
}

const cardGroups = computed(() => {
  const types = ['5h', 'week']
  return types
    .map((type) => {
      const groupCards = cards.value.filter((card) => card.type === type)
      const earliest = earliestExpiry(groupCards)
      return {
        type,
        title: typeLabel(type),
        cards: groupCards,
        earliestLabel: earliest ? formatDateTime(new Date(earliest).toISOString()) : '-'
      }
    })
    .filter((group) => group.cards.length > 0)
})

function remainingText(card: ZCodeResetCard): string {
  const expires = new Date(card.expires_at).getTime()
  if (Number.isNaN(expires)) return '-'
  const diff = expires - Date.now()
  if (diff <= 0) return t('admin.accounts.zcode.resetCards.expired')
  const hours = Math.floor(diff / 3600000)
  if (hours >= 24) {
    return t('admin.accounts.zcode.resetCards.remainingDays', { days: Math.floor(hours / 24), hours: hours % 24 })
  }
  return t('admin.accounts.zcode.resetCards.remainingHours', { hours })
}

async function loadCards(force = false) {
  if (!props.account) return
  loading.value = true
  try {
    const inventory = await zcodeAPI.getResetCards(props.account.id, force)
    cards.value = inventory?.cards ?? []
    observedAt.value = inventory?.observed_at ?? ''
  } catch (error: any) {
    appStore.showError(error?.response?.data?.message || error?.message || String(error))
  } finally {
    loading.value = false
  }
}

async function loadUsage() {
  if (!props.account) return
  usageLoading.value = true
  try {
    usage.value = await adminAPI.accounts.getUsage(props.account.id, 'active', true)
  } catch (error: any) {
    // 额度查询失败不阻塞重置卡操作
    console.warn('Failed to load zcode quota:', error?.message || error)
    usage.value = null
  } finally {
    usageLoading.value = false
  }
}

const reload = () => {
  void loadCards(true)
  void loadUsage()
}

function syncSettingsFromAccount() {
  autoClaim.value = accountExtra.value.zcode_auto_claim !== false
  peakPause.value = accountExtra.value.zcode_peak_pause === true
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

async function useCard(type: string) {
  if (!props.account) return
  using.value = type
  actionError.value = ''
  try {
    const inventory = await zcodeAPI.useResetCard(props.account.id, type)
    cards.value = inventory?.cards ?? []
    observedAt.value = inventory?.observed_at ?? ''
    appStore.showSuccess(t('admin.accounts.zcode.resetCards.used'))
    void loadUsage()
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
    syncSettingsFromAccount()
    cards.value = []
    usage.value = null
    actionError.value = ''
    reload()
  }
)
</script>
