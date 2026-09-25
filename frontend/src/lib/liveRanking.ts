import type { AdminStats } from '../api/types.ts'

export type RankingRow = NonNullable<AdminStats['ranking']>['items'][number]
export const rankingScore = (row: RankingRow, key: string) => key === 'total' ? row.total : row.categoryScores?.[key] ?? null
export const rankingPlace = (row: RankingRow, key: string) => key === 'total' ? row.classRank ?? null : row.categoryRanks?.[key] ?? null

export function orderedRanking(rows: RankingRow[], key: string, search = '') {
  const query = search.trim().toLocaleLowerCase()
  return rows.filter((row) => `${row.name} ${row.sid}`.toLocaleLowerCase().includes(query)).sort((a, b) => {
    const av = rankingScore(a, key), bv = rankingScore(b, key)
    if (av == null && bv != null) return 1
    if (bv == null && av != null) return -1
    return (bv ?? 0) - (av ?? 0) || a.sid.localeCompare(b.sid)
  })
}

export function rankingSummary(rows: RankingRow[], key: string) {
  const values = rows.map((row) => rankingScore(row, key)).filter((value): value is number => value != null)
  return { count: values.length, average: values.length ? values.reduce((sum, value) => sum + value, 0) / values.length : null,
    max: values.length ? Math.max(...values) : null, min: values.length ? Math.min(...values) : null }
}
