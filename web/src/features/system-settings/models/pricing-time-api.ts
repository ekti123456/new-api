import { api } from '@/lib/api'

export async function getPricingTime(): Promise<boolean> {
  const response = await api.get<{
    success: boolean
    data?: { cn_off_peak?: boolean }
  }>('/api/ratio_sync/pricing-time')
  if (
    !response.data.success ||
    typeof response.data.data?.cn_off_peak !== 'boolean'
  ) {
    throw new Error('Pricing time is unavailable')
  }
  return response.data.data.cn_off_peak
}
