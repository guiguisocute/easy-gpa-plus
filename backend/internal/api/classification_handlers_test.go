package api

import "testing"

func TestClassificationResolutionRequiresArbitration(t *testing.T) {
	tests := []struct {
		name            string
		beforeCategory  string
		targetCategory  string
		suggestionScope string
		want            bool
	}{
		{name: "within-category suggestion can be resolved before export", beforeCategory: "moral", targetCategory: "moral", suggestionScope: "within_category", want: false},
		{name: "cross-category suggestion requires arbitration even when kept", beforeCategory: "moral", targetCategory: "moral", suggestionScope: "cross_category", want: true},
		{name: "cross-category target without suggestion requires arbitration", beforeCategory: "moral", targetCategory: "practice", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classificationResolutionRequiresArbitration(tt.beforeCategory, tt.targetCategory, tt.suggestionScope); got != tt.want {
				t.Fatalf("classificationResolutionRequiresArbitration() = %v, want %v", got, tt.want)
			}
		})
	}
}
