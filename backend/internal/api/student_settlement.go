package api

import "encoding/json"

// Project stored settlements at the student response boundary, including old
// and stale runs. Keep the original snapshot intact for administrator auditing.
func studentSettlementDetails(raw []byte) (json.RawMessage, error) {
	var details map[string]json.RawMessage
	if err := json.Unmarshal(raw, &details); err != nil {
		return nil, err
	}
	if base, ok := details["baseItems"]; ok {
		var items []map[string]json.RawMessage
		if err := json.Unmarshal(base, &items); err != nil {
			return nil, err
		}
		for i, item := range items {
			// Allow only student-facing fields: future internal identity fields
			// must not become public merely by being added to the snapshot.
			public := make(map[string]json.RawMessage)
			for _, key := range []string{"id", "category", "itemKey", "name", "kind", "fullScore", "score", "basis", "recorded", "appeals"} {
				if value, ok := item[key]; ok {
					public[key] = value
				}
			}
			items[i] = public
		}
		base, err := json.Marshal(items)
		if err != nil {
			return nil, err
		}
		details["baseItems"] = base
	}
	return json.Marshal(details)
}
