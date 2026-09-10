export interface ExpansionPolicy {
  enabled: boolean
  extra_limit: number
  multiplier: number
  channel_ids: number[]
}

export interface PersonalWindow {
  id: string
  created_at: string
  expires_at: string
  model?: string
  expanded: boolean
  multiplier: number
  grant_id?: string
  can_upgrade?: boolean
  upgraded_at?: string
}

export interface WindowPool {
  id: number
  name: string
  reference?: string
  available: boolean
  error?: string
  status: {
    limit: number
    used: number
    truncated?: boolean
    windows: PersonalWindow[] | null
    creation_available_at?: string | null
    cooldown_unavailable: boolean
  }
}

export interface PersonalWindowsData {
  enabled: boolean
  accepted_multiplier: number
  policy: ExpansionPolicy
  pools: WindowPool[]
  updated_at: string
  server_now: string
}
