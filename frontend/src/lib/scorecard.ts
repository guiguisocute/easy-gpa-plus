

// Historical frozen snapshots remain intact, but irreversibly rejected
// submissions are no longer review targets. An ordinary zero is still a target.
export const isScorecardReviewItem = (item: { forceRejection?: unknown }) => !item.forceRejection
