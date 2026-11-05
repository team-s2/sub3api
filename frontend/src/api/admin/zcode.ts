/**
 * Admin ZCode (Z.ai / BigModel) OAuth endpoints.
 *
 * 登录成功后账号固定走 ultra 端点（CodingPlan key 鉴权，官方 1.5x 配额口径），
 * 账号本身按正常倍率计费。
 */

import { apiClient } from '../client'

/** 可选登录入口。 */
export interface ZCodeSiteOption {
  id: string
  name: string
  description: string
}

/** 发起授权的结果。 */
export interface ZCodeAuthUrlResponse {
  auth_url: string
  state: string
  site: string
  expires_at: number
}

/** 轮询状态。 */
export type ZCodePollStatus = 'pending' | 'ready' | 'declined'

export interface ZCodePollResponse {
  status: ZCodePollStatus
  site: string
  expires_at: number
  message?: string
  email?: string
  name?: string
  user_id?: string
  plan?: string
  team?: boolean
  has_api_key: boolean
  masked_api_key?: string
  warning?: string
}

export interface ZCodeAuthUrlRequest {
  site: string
  proxy_id?: number | null
}

export interface ZCodeCreateAccountRequest {
  state: string
  name?: string
  notes?: string
  api_key?: string
  proxy_id?: number | null
  concurrency?: number
  priority?: number
  group_ids?: number[]
}

const ZCODE_AUTHORIZATION_TIMEOUT_MS = 120_000

export async function getSites(): Promise<ZCodeSiteOption[]> {
  const { data } = await apiClient.get<ZCodeSiteOption[]>('/admin/zcode/oauth/sites')
  return data
}

export async function generateAuthUrl(payload: ZCodeAuthUrlRequest): Promise<ZCodeAuthUrlResponse> {
  const { data } = await apiClient.post<ZCodeAuthUrlResponse>('/admin/zcode/oauth/auth-url', payload, {
    timeout: ZCODE_AUTHORIZATION_TIMEOUT_MS
  })
  return data
}

export async function poll(state: string): Promise<ZCodePollResponse> {
  const { data } = await apiClient.post<ZCodePollResponse>('/admin/zcode/oauth/poll', { state }, {
    timeout: ZCODE_AUTHORIZATION_TIMEOUT_MS
  })
  return data
}

/** 用已完成的登录创建账号；未传 api_key 时用自动开通的 CodingPlan key。 */
export async function createAccount(payload: ZCodeCreateAccountRequest): Promise<unknown> {
  const { data } = await apiClient.post<unknown>('/admin/zcode/oauth/accounts', payload, {
    timeout: ZCODE_AUTHORIZATION_TIMEOUT_MS
  })
  return data
}

export default {
  getSites,
  generateAuthUrl,
  poll,
  createAccount
}
