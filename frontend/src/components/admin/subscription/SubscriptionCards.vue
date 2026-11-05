<template>
  <section class="min-h-0">
    <!-- 工具条 -->
    <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
      <h2 class="text-base font-semibold text-gray-900 dark:text-white">
        {{ t('nav.subscriptions') }}
      </h2>
      <div class="flex flex-wrap items-center gap-2">
        <select v-model="statusFilter" class="input h-8 w-32 py-0 text-xs" @change="reload">
          <option value="">{{ t('admin.accounts.zcode.plans.allStatus') }}</option>
          <option value="active">{{ t('admin.subscriptions.status.active') }}</option>
          <option value="expired">{{ t('admin.subscriptions.status.expired') }}</option>
          <option value="revoked">{{ t('admin.subscriptions.status.revoked') }}</option>
        </select>
        <button type="button" class="btn btn-secondary text-xs" :disabled="loading" @click="reload">
          <Icon name="refresh" size="sm" :class="['mr-1', loading ? 'animate-spin' : '']" />
          {{ t('common.refresh') }}
        </button>
      </div>
    </div>

    <!-- 方块网格 -->
    <div v-if="loading && !items.length" class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('common.loading') }}
    </div>
    <div
      v-else-if="!items.length"
      class="rounded-xl border border-dashed border-gray-300 py-8 text-center text-sm text-gray-500 dark:border-dark-600 dark:text-gray-400"
    >
      {{ t('admin.accounts.zcode.plans.empty') }}
    </div>
    <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-3 2xl:grid-cols-4">
      <button
        v-for="item in items"
        :key="item.id"
        type="button"
        class="flex flex-col rounded-xl border border-gray-200 p-3 text-left transition-colors hover:border-primary-400 hover:shadow-sm dark:border-dark-600 dark:hover:border-primary-500"
        @click="openDetail(item)"
      >
        <!-- 标题行 -->
        <div class="flex items-start justify-between gap-2">
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-gray-900 dark:text-gray-100">{{ userLabel(item) }}</p>
            <p class="truncate text-xs text-gray-500 dark:text-gray-400">{{ item.group?.name || t('admin.accounts.zcode.plans.noGroup') }}</p>
          </div>
          <span class="shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium" :class="statusClass(item.status)">
            {{ statusLabel(item.status) }}
          </span>
        </div>

        <!-- 用量 -->
        <div class="mt-3 space-y-1.5">
          <div v-for="usage in usageRows(item)" :key="usage.label" class="text-[11px] text-gray-500 dark:text-gray-400">
            <div class="flex items-center justify-between">
              <span>{{ usage.label }}</span>
              <span class="tabular-nums text-gray-700 dark:text-gray-300">{{ usage.text }}</span>
            </div>
          </div>
        </div>

        <div class="mt-3 flex items-center justify-between border-t border-gray-100 pt-2 dark:border-dark-700">
          <span class="text-[11px] text-gray-500 dark:text-gray-400">
            {{ t('admin.accounts.zcode.plans.expires') }} {{ formatDateTime(item.expires_at) }}
          </span>
          <span class="inline-flex items-center gap-0.5 text-xs font-medium text-primary-600 dark:text-primary-400">
            {{ t('admin.accounts.zcode.plans.detail') }}
            <Icon name="chevronRight" size="xs" />
          </span>
        </div>
      </button>
    </div>

    <!-- 分页 -->
    <div v-if="total > pageSize" class="mt-3 flex items-center justify-between text-xs text-gray-500 dark:text-gray-400">
      <span>{{ t('admin.accounts.zcode.plans.totalCount', { total }) }}</span>
      <div class="flex items-center gap-2">
        <button type="button" class="btn btn-secondary text-xs" :disabled="page <= 1 || loading" @click="changePage(-1)">
          {{ t('admin.accounts.zcode.plans.prevPage') }}
        </button>
        <span class="tabular-nums">{{ page }} / {{ totalPages }}</span>
        <button type="button" class="btn btn-secondary text-xs" :disabled="page >= totalPages || loading" @click="changePage(1)">
          {{ t('admin.accounts.zcode.plans.nextPage') }}
        </button>
      </div>
    </div>

    <!-- 详情弹窗 -->
    <BaseDialog :show="detailVisible" :title="detail ? userLabel(detail) : ''" width="wide" @close="detailVisible = false">
      <div v-if="detail" class="space-y-3">
        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.plans.group') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ detail.group?.name || '-' }}</p>
          </div>
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.plans.status') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ statusLabel(detail.status) }}</p>
          </div>
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.plans.startsAt') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ formatDateTime(detail.starts_at) }}</p>
          </div>
          <div class="rounded-xl border border-gray-200 p-3 dark:border-dark-600">
            <p class="text-xs text-gray-500 dark:text-gray-400">{{ t('admin.accounts.zcode.plans.expires') }}</p>
            <p class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ formatDateTime(detail.expires_at) }}</p>
          </div>
        </div>

        <div class="rounded-xl border border-gray-200 dark:border-dark-600">
          <div class="border-b border-gray-100 px-3 py-2 text-xs font-medium text-gray-500 dark:border-dark-700 dark:text-gray-400">
            {{ t('admin.accounts.zcode.plans.usage') }}
          </div>
          <div class="divide-y divide-gray-100 dark:divide-dark-700">
            <div
              v-for="usage in usageRows(detail)"
              :key="usage.label"
              class="flex items-center justify-between px-3 py-2 text-sm"
            >
              <span class="text-gray-600 dark:text-gray-300">{{ usage.label }}</span>
              <span class="tabular-nums font-medium text-gray-900 dark:text-gray-100">{{ usage.text }}</span>
            </div>
          </div>
        </div>

        <div
          v-if="actionError"
          class="rounded-lg border border-red-200 bg-red-50 p-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300"
        >
          {{ actionError }}
        </div>

        <div class="flex flex-wrap gap-2">
          <button type="button" class="btn btn-secondary text-xs" :disabled="acting" @click="act('extend')">
            {{ t('admin.accounts.zcode.plans.extend30') }}
          </button>
          <button type="button" class="btn btn-secondary text-xs" :disabled="acting" @click="act('reset')">
            {{ t('admin.accounts.zcode.plans.resetQuota') }}
          </button>
          <button
            v-if="detail.status === 'revoked'"
            type="button"
            class="btn btn-secondary text-xs"
            :disabled="acting"
            @click="act('restore')"
          >
            {{ t('admin.accounts.zcode.plans.restore') }}
          </button>
          <button v-else type="button" class="btn btn-secondary text-xs !text-red-600" :disabled="acting" @click="act('revoke')">
            {{ t('admin.accounts.zcode.plans.revoke') }}
          </button>
        </div>
      </div>
    </BaseDialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { adminAPI } from '@/api/admin'
import { useAppStore } from '@/stores/app'
import type { UserSubscription } from '@/types'

const { t } = useI18n()
const appStore = useAppStore()

const loading = ref(false)
const acting = ref(false)
const items = ref<UserSubscription[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(12)
const statusFilter = ref('')
const detailVisible = ref(false)
const detail = ref<UserSubscription | null>(null)
const actionError = ref('')

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / pageSize.value)))

function formatDateTime(value?: string | null): string {
  if (!value) return '-'
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return String(value)
  return d.toLocaleString()
}

function userLabel(item: UserSubscription): string {
  return item.user?.email || item.user?.username || `#${item.user_id}`
}

function statusLabel(status: string): string {
  switch (status) {
    case 'active':
      return t('admin.subscriptions.status.active')
    case 'expired':
      return t('admin.subscriptions.status.expired')
    case 'revoked':
      return t('admin.subscriptions.status.revoked')
    case 'suspended':
      return t('admin.subscriptions.status.suspended')
    default:
      return status
  }
}

function statusClass(status: string): string {
  switch (status) {
    case 'active':
      return 'bg-emerald-100 text-emerald-700 dark:bg-emerald-900/40 dark:text-emerald-300'
    case 'expired':
      return 'bg-gray-100 text-gray-600 dark:bg-dark-700 dark:text-gray-300'
    case 'revoked':
      return 'bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-300'
    default:
      return 'bg-amber-100 text-amber-700 dark:bg-amber-900/40 dark:text-amber-300'
  }
}

function money(value?: number | null): string {
  const n = Number(value ?? 0)
  return `$${n.toFixed(n < 1 ? 4 : 2)}`
}

function usageRows(item: UserSubscription): Array<{ label: string; text: string }> {
  return [
    { label: t('admin.accounts.zcode.plans.dailyUsage'), text: money(item.daily_usage_usd) },
    { label: t('admin.accounts.zcode.plans.weeklyUsage'), text: money(item.weekly_usage_usd) },
    { label: t('admin.accounts.zcode.plans.monthlyUsage'), text: money(item.monthly_usage_usd) }
  ]
}

async function load() {
  loading.value = true
  try {
    const result = await adminAPI.subscriptions.list(page.value, pageSize.value, {
      status: (statusFilter.value || undefined) as UserSubscription['status'] | undefined,
      sort_by: 'created_at',
      sort_order: 'desc'
    })
    items.value = result.items ?? []
    total.value = result.total ?? 0
  } catch (error: any) {
    appStore.showError(error?.response?.data?.message || error?.message || String(error))
  } finally {
    loading.value = false
  }
}

function reload() {
  page.value = 1
  void load()
}

function changePage(delta: number) {
  page.value = Math.min(Math.max(1, page.value + delta), totalPages.value)
  void load()
}

function openDetail(item: UserSubscription) {
  detail.value = item
  actionError.value = ''
  detailVisible.value = true
}

async function act(kind: 'extend' | 'reset' | 'revoke' | 'restore') {
  if (!detail.value) return
  acting.value = true
  actionError.value = ''
  try {
    if (kind === 'extend') {
      detail.value = await adminAPI.subscriptions.extend(detail.value.id, { days: 30 })
    } else if (kind === 'reset') {
      detail.value = await adminAPI.subscriptions.resetQuota(detail.value.id, { daily: true, weekly: true, monthly: true })
    } else if (kind === 'revoke') {
      await adminAPI.subscriptions.revoke(detail.value.id)
      detail.value = { ...detail.value, status: 'revoked' }
    } else {
      detail.value = await adminAPI.subscriptions.restore(detail.value.id)
    }
    appStore.showSuccess(t('admin.accounts.zcode.plans.actionDone'))
    await load()
  } catch (error: any) {
    actionError.value = error?.response?.data?.message || error?.message || String(error)
  } finally {
    acting.value = false
  }
}

onMounted(load)

defineExpose({ reload })
</script>
