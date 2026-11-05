<template>
  <BaseDialog :show="show" :title="t('admin.accounts.zcode.resetCards.title')" width="wide" @close="handleClose">
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
          <button type="button" class="btn btn-secondary text-xs" :disabled="loading" @click="load(true)">
            <Icon name="refresh" size="sm" :class="['mr-1', loading ? 'animate-spin' : '']" />
            {{ t('common.refresh') }}
          </button>
        </div>
      </div>

      <!-- 卡片方块 -->
      <div v-if="loading && !cards.length" class="py-10 text-center text-sm text-gray-500 dark:text-gray-400">
        {{ t('common.loading') }}
      </div>
      <div v-else-if="!cards.length" class="rounded-xl border border-dashed border-gray-300 py-10 text-center dark:border-dark-600">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.empty') }}</p>
        <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ t('admin.accounts.zcode.resetCards.emptyHint') }}</p>
      </div>
      <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3">
        <button
          v-for="card in cards"
          :key="card.type + card.expires_at"
          type="button"
          class="group flex flex-col rounded-xl border border-gray-200 p-3 text-left transition-colors hover:border-primary-400 hover:bg-primary-50/40 dark:border-dark-600 dark:hover:border-primary-500 dark:hover:bg-primary-900/10"
          :class="{ 'border-primary-500 bg-primary-50/40 dark:bg-primary-900/10': expandedType === card.type }"
          @click="openDetail(card)"
        >
          <div class="flex items-start justify-between gap-2">
            <span class="text-sm font-semibold text-gray-900 dark:text-gray-100">
              {{ typeLabel(card.type) }}
            </span>
            <span
              class="rounded-full px-2 py-0.5 text-[11px] font-medium"
              :class="
                isExpiringSoon(card)
                  ? 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
                  : 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
              "
            >
              {{ isExpiringSoon(card) ? t('admin.accounts.zcode.resetCards.expiringSoon') : t('admin.accounts.zcode.resetCards.available') }}
            </span>
          </div>
          <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.zcode.resetCards.expiresAt') }}
          </p>
          <p class="text-sm font-medium text-gray-800 dark:text-gray-200">{{ formatDateTime(card.expires_at) }}</p>
          <p class="mt-1 text-[11px] text-gray-400 dark:text-gray-500">{{ remainingText(card) }}</p>
          <span class="mt-3 inline-flex items-center gap-1 text-xs font-medium text-primary-600 dark:text-primary-400">
            {{ t('admin.accounts.zcode.resetCards.detail') }}
            <Icon name="chevronRight" size="xs" />
          </span>
        </button>
      </div>
    </div>

    <!-- 单卡详情弹窗 -->
    <BaseDialog
      :show="detailVisible"
      :title="detailCard ? typeLabel(detailCard.type) : ''"
      width="normal"
      @close="detailVisible = false"
    >
      <div v-if="detailCard" class="space-y-3">
        <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
          <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.window') }}</p>
          <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ typeDescription(detailCard.type) }}</p>
        </div>
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.expiresAt') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ formatDateTime(detailCard.expires_at) }}</p>
          </div>
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.remaining') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ remainingText(detailCard) }}</p>
          </div>
        </div>
        <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.resetCards.useHint') }}</p>
        <div
          v-if="actionError"
          class="rounded-lg border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
        >
          {{ actionError }}
        </div>
      </div>
      <template #footer>
        <div class="flex justify-end gap-2">
          <button type="button" class="btn btn-secondary" @click="detailVisible = false">
            {{ t('common.cancel') }}
          </button>
          <button type="button" class="btn btn-primary" :disabled="using" @click="useCard">
            <Icon v-if="using" name="refresh" size="sm" class="mr-1 animate-spin" />
            {{ t('admin.accounts.zcode.resetCards.use') }}
          </button>
        </div>
      </template>
    </BaseDialog>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import zcodeAPI, { type ZCodeResetCard } from '@/api/admin/zcode'
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
const using = ref(false)
const cards = ref<ZCodeResetCard[]>([])
const observedAt = ref<string>('')
const autoClaim = ref(true)
const peakPause = ref(false)
const detailVisible = ref(false)
const detailCard = ref<ZCodeResetCard | null>(null)
const actionError = ref('')
const expandedType = ref('')

const observedAtText = computed(() => (observedAt.value ? formatDateTime(observedAt.value) : '-'))
const accountExtra = computed(() => (props.account?.extra ?? {}) as Record<string, unknown>)

function formatDateTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  return d.toLocaleString()
}

function typeLabel(type: string): string {
  return type === '5h'
    ? t('admin.accounts.zcode.resetCards.type5h')
    : t('admin.accounts.zcode.resetCards.typeWeek')
}

function typeDescription(type: string): string {
  return type === '5h'
    ? t('admin.accounts.zcode.resetCards.type5hDesc')
    : t('admin.accounts.zcode.resetCards.typeWeekDesc')
}

function isExpiringSoon(card: ZCodeResetCard): boolean {
  const expires = new Date(card.expires_at).getTime()
  if (Number.isNaN(expires)) return false
  return expires - Date.now() < 24 * 3600 * 1000
}

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

async function load(force = false) {
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

function openDetail(card: ZCodeResetCard) {
  detailCard.value = card
  expandedType.value = card.type
  actionError.value = ''
  detailVisible.value = true
}

async function useCard() {
  if (!props.account || !detailCard.value) return
  using.value = true
  actionError.value = ''
  try {
    const inventory = await zcodeAPI.useResetCard(props.account.id, detailCard.value.type)
    cards.value = inventory?.cards ?? []
    observedAt.value = inventory?.observed_at ?? ''
    appStore.showSuccess(t('admin.accounts.zcode.resetCards.used'))
    detailVisible.value = false
  } catch (error: any) {
    actionError.value = error?.response?.data?.message || error?.message || String(error)
  } finally {
    using.value = false
  }
}

function handleClose() {
  detailVisible.value = false
  emit('close')
}

watch(
  () => props.show,
  (visible) => {
    if (!visible) return
    syncSettingsFromAccount()
    cards.value = []
    void load(true)
  }
)
</script>
