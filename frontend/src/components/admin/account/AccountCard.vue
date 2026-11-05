<template>
  <div
    class="group relative flex cursor-pointer flex-col gap-2.5 rounded-xl border border-gray-200 bg-white p-3 transition-colors hover:border-primary-300 hover:shadow-sm dark:border-dark-600 dark:bg-dark-800 dark:hover:border-primary-700"
    :class="[
      selected
        ? 'border-primary-400 bg-primary-50/60 dark:border-primary-600 dark:bg-primary-900/20'
        : '',
      account.schedulable ? '' : 'opacity-70'
    ]"
    data-testid="account-card"
    @click="emit('edit', account)"
  >
    <!-- 第一行：勾选 + 平台/类型徽标 + 状态 + 更多操作 -->
    <div class="flex items-start justify-between gap-2">
      <div class="flex min-w-0 flex-1 items-center gap-2">
        <input
          type="checkbox"
          class="mt-0.5 h-3.5 w-3.5 shrink-0 cursor-pointer rounded border-gray-300 text-primary-600 focus:ring-primary-500"
          :checked="selected"
          :aria-label="account.name"
          @click.stop
          @change="emit('toggle-select', account.id)"
        />
        <div class="min-w-0 overflow-hidden">
          <PlatformTypeBadge
            :platform="account.platform"
            :type="account.type"
            :zcode-oauth="isZCode"
            :auth-mode="authMode ?? undefined"
            :plan-type="planType ?? undefined"
            :privacy-mode="(account.extra?.privacy_mode as string | undefined) ?? account.parent_privacy_mode ?? undefined"
            :subscription-expires-at="(account.credentials?.subscription_expires_at as string | undefined) ?? account.parent_subscription_expires_at ?? undefined"
          />
        </div>
      </div>
      <div class="flex shrink-0 items-center gap-1">
        <AccountStatusIndicator :account="account as Account" @show-temp-unsched="(a) => emit('show-temp-unsched', a)" />
        <button
          type="button"
          class="rounded-md p-1 text-gray-400 transition-colors hover:bg-red-50 hover:text-red-600 dark:hover:bg-red-900/20 dark:hover:text-red-400"
          :title="t('common.delete')"
          data-testid="account-card-delete"
          @click.stop="emit('delete', account)"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M14.74 9l-.346 9m-4.788 0L9.26 9m9.968-3.21c.342.052.682.107 1.022.166m-1.022-.165L18.16 19.673a2.25 2.25 0 01-2.244 2.077H8.084a2.25 2.25 0 01-2.244-2.077L4.772 5.79m14.456 0a48.108 48.108 0 00-3.478-.397m-12 .562c.34-.059.68-.114 1.022-.165m0 0a48.11 48.11 0 013.478-.397m7.5 0v-.916c0-1.18-.91-2.164-2.09-2.201a51.964 51.964 0 00-3.32 0c-1.18.037-2.09 1.022-2.09 2.201v.916m7.5 0a48.667 48.667 0 00-7.5 0" />
          </svg>
        </button>
        <button
          type="button"
          class="-mr-1 rounded-md p-1 text-gray-400 transition-colors hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-dark-700 dark:hover:text-gray-200"
          :title="t('common.more')"
          data-testid="account-card-menu"
          @click.stop="emit('menu', account, $event)"
        >
          <svg class="h-4 w-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M6.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM12.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0zM18.75 12a.75.75 0 11-1.5 0 .75.75 0 011.5 0z" />
          </svg>
        </button>
      </div>
    </div>

    <!-- 第二行：名称 + ID + 邮箱 -->
    <div class="min-w-0">
      <div class="flex min-w-0 items-baseline gap-1.5">
        <span class="shrink-0 font-mono text-[11px] text-gray-400 dark:text-dark-400">#{{ account.id }}</span>
        <span class="truncate text-sm font-semibold text-gray-900 dark:text-white" :title="account.name">
          {{ account.name }}
        </span>
      </div>
      <div
        v-if="email"
        class="truncate text-xs text-gray-500 dark:text-dark-400"
        :title="email"
      >
        {{ email }}
      </div>
      <div v-else-if="account.notes" class="truncate text-xs text-gray-400 dark:text-dark-400" :title="account.notes">
        {{ account.notes }}
      </div>
    </div>

    <!-- 第四行：分组 + 调度开关 + 详情 -->
    <div class="mt-auto flex items-center justify-between gap-2 border-t border-gray-100 pt-2 dark:border-dark-700">
      <div class="min-w-0 flex-1 overflow-hidden">
        <AccountGroupsCell :groups="groups" :max-display="2" />
      </div>
      <div class="flex shrink-0 items-center gap-2">
        <button
          v-if="isZCode"
          type="button"
          class="rounded-md border border-primary-200 px-1.5 py-0.5 text-[11px] font-medium text-primary-600 transition-colors hover:bg-primary-50 dark:border-primary-800 dark:text-primary-300 dark:hover:bg-primary-900/30"
          data-testid="account-card-reset-cards"
          @click.stop="emit('quota', account as Account)"
        >
          {{ t('admin.accounts.zcode.quota.button') }}
        </button>
        <span class="hidden text-[11px] text-gray-400 sm:inline">{{
          account.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled')
        }}</span>
        <button
          type="button"
          :disabled="togglingSchedulable"
          class="relative inline-flex h-4 w-8 flex-shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors duration-200 disabled:cursor-not-allowed disabled:opacity-50"
          :class="[
            account.schedulable
              ? 'bg-primary-500 hover:bg-primary-600'
              : 'bg-gray-200 hover:bg-gray-300 dark:bg-dark-600 dark:hover:bg-dark-500'
          ]"
          :title="account.schedulable ? t('admin.accounts.schedulableEnabled') : t('admin.accounts.schedulableDisabled')"
          @click.stop="emit('toggle-schedulable', account)"
        >
          <span
            class="pointer-events-none inline-block h-3 w-3 transform rounded-full bg-white shadow ring-0 transition duration-200"
            :class="account.schedulable ? 'translate-x-4' : 'translate-x-0'"
          />
        </button>
        <span class="text-[11px] font-medium text-primary-600 dark:text-primary-400">{{ t('admin.accounts.card.details') }}</span>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import type { Account, AccountListItem, AdminGroup } from '@/types'
import PlatformTypeBadge from '@/components/common/PlatformTypeBadge.vue'
import AccountStatusIndicator from '@/components/account/AccountStatusIndicator.vue'
import AccountGroupsCell from '@/components/account/AccountGroupsCell.vue'

const props = withDefaults(
  defineProps<{
    account: AccountListItem
    groups: AdminGroup[]
    selected?: boolean
    email?: string | null
    authMode?: string | null
    planType?: string | null
    togglingSchedulable?: boolean
  }>(),
  {
    selected: false,
    email: null,
    authMode: null,
    planType: null,
    togglingSchedulable: false
  }
)

const emit = defineEmits<{
  (e: 'edit', account: AccountListItem): void
  (e: 'menu', account: AccountListItem, event: MouseEvent): void
  (e: 'toggle-select', id: number): void
  (e: 'toggle-schedulable', account: AccountListItem): void
  (e: 'show-temp-unsched', account: Account): void
  (e: 'quota', account: Account): void
  (e: 'delete', account: AccountListItem): void
}>()

const { t } = useI18n()

// ZCode 账号（走 ultra 端点）才提供重置卡入口。
const isZCode = computed(
  () => props.account.platform === 'zhipu' && Boolean(props.account.credentials?.zcode_user_id)
)
</script>
