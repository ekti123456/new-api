export type TierConditionInput = {
  var: 'p' | 'c' | 'len' | 'channel_id'
  op: '<' | '<=' | '>' | '>=' | '==' | '!='
  value: number | string
}

const numericCondition = String.raw`(?:p|c|len)\s*(?:<=|>=|<|>)\s*[\d.eE+]+`
const channelCondition = String.raw`channel_id\s*(?:==|!=)\s*\d+`
const conditionPattern = `(?:${numericCondition}|${channelCondition})`

export const TIER_CONDITIONS_PATTERN = `(${conditionPattern}(?:\\s*&&\\s*${conditionPattern})*)`

export function parseTierConditions(source: string): TierConditionInput[] {
  const conditions: TierConditionInput[] = []
  for (const match of source.matchAll(new RegExp(conditionPattern, 'g'))) {
    const conditionText = match[0].trim()
    const channelMatch = conditionText.match(/^channel_id\s*(==|!=)\s*(\d+)$/)
    if (channelMatch) {
      conditions.push({
        var: 'channel_id',
        op: channelMatch[1] as TierConditionInput['op'],
        value: Number(channelMatch[2]),
      })
      continue
    }
    const numericMatch = conditionText.match(
      /^(p|c|len)\s*(<=|>=|<|>)\s*([\d.eE+]+)$/
    )
    if (numericMatch) {
      conditions.push({
        var: numericMatch[1] as TierConditionInput['var'],
        op: numericMatch[2] as TierConditionInput['op'],
        value: Number(numericMatch[3]),
      })
    }
  }
  return conditions
}
