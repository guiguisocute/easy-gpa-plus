import { AdjudicationContext } from '@/api/adjudicationScope'
import AdminArbitration from '@/pages/admin/Arbitration'

export default function DeputyArbitration() {
  return <AdjudicationContext.Provider value="deputy"><AdminArbitration /></AdjudicationContext.Provider>
}
