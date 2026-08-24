/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import * as z from 'zod'

export const errorRateLockSchema = z.object({
  user_error_rate_lock_enabled: z.boolean(),
  user_error_rate_lock_min_requests: z.coerce.number().int().min(1).max(100000),
  user_error_rate_lock_threshold: z.coerce.number().gt(0).max(100),
  user_error_rate_lock_seconds: z.coerce.number().int().min(1).max(86400),
})
