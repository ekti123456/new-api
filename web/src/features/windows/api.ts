import { api } from '@/lib/api'

import type { ExpansionPolicy, PersonalWindowsData } from './types'

export async function getPersonalWindows(): Promise<PersonalWindowsData> {
  const response = await api.get<{
    success: boolean
    data: PersonalWindowsData
    message?: string
  }>('/api/user/windows')
  if (!response.data.success) {
    throw new Error(response.data.message || 'Window service unavailable')
  }
  return response.data.data
}

export async function saveExpansion(
  enabled: boolean,
  accepted_multiplier: number
): Promise<void> {
  const response = await api.put('/api/user/windows/expansion', {
    enabled,
    accepted_multiplier,
  })
  if (!response.data.success) {
    throw new Error(response.data.message || 'Failed to save settings')
  }
}

export async function saveExpansionPolicy(
  policy: ExpansionPolicy
): Promise<void> {
  const response = await api.put('/api/user/windows/policy', policy)
  if (!response.data.success) {
    throw new Error(response.data.message || 'Failed to save settings')
  }
}
