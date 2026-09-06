import {
  buildRequestRuleExpr,
  tryParseRequestRuleExpr,
  type RequestRuleGroup,
} from './billing-expr'

export type RequestRuleMatch = {
  expression: string
  matched: boolean
}

function billingRuleKey(group: RequestRuleGroup): string {
  return buildRequestRuleExpr([
    { ...group, multiplier: String(Number(group.multiplier)) },
  ])
}

export function matchBillingRuleGroups(
  groups: RequestRuleGroup[],
  matches?: RequestRuleMatch[]
): (RequestRuleGroup & { key: string; matched: boolean | undefined })[] {
  const recordedMatches = new Map<string, boolean | undefined>()
  for (const match of matches ?? []) {
    const parsed = tryParseRequestRuleExpr(`(${match.expression})`)
    if (parsed?.length !== 1) continue
    const key = billingRuleKey(parsed[0])
    if (
      recordedMatches.has(key) &&
      recordedMatches.get(key) !== match.matched
    ) {
      recordedMatches.set(key, undefined)
    } else {
      recordedMatches.set(key, match.matched)
    }
  }
  const occurrences = new Map<string, number>()
  return groups.map((group) => {
    const expression = billingRuleKey(group)
    const occurrence = (occurrences.get(expression) ?? 0) + 1
    occurrences.set(expression, occurrence)
    return {
      ...group,
      key: `${expression}#${occurrence}`,
      matched: recordedMatches.get(expression),
    }
  })
}
