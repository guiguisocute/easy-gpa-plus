package scheme

import "encoding/json"

// Template is the reusable scoring part of a scheme. Runtime state belongs to
// a class, not to the platform template library: dates, capability switches,
// honor-roll policy and the generated version are deliberately absent here.
type Template struct {
	SchemeName string             `json:"schemeName"`
	Weights    map[string]float64 `json:"weights"`
	Categories []Category         `json:"categories"`
}

// TemplateFromConfig removes class-specific runtime state before a published
// scheme is shared or imported into the platform library.
func TemplateFromConfig(config Config) Template {
	return Template{
		SchemeName: config.SchemeName,
		Weights:    config.Weights,
		Categories: stripActivities(config.Categories),
	}
}

// stripActivities drops the per-year activity roster on the way into the
// template library. Scoring rules are worth reusing next year; the list of
// events this class actually held is not, and silently carrying it forward
// would show students last year's 活动 as if they were still open.
//
// Copies rather than mutating: the caller still holds the live config, and a
// published scheme must not lose its roster just because someone exported it.
func stripActivities(categories []Category) []Category {
	out := make([]Category, len(categories))
	for i, category := range categories {
		items := make([]Item, len(category.Items))
		for j, item := range category.Items {
			item.Activities = nil
			items[j] = item
		}
		category.Items = items
		out[i] = category
	}
	return out
}

// DecodeTemplate accepts both the canonical template shape and legacy full
// scheme JSON. encoding/json ignores the old runtime fields, which makes old
// exported files safe to re-import while new rows stay runtime-free.
func DecodeTemplate(raw []byte) (Template, error) {
	var template Template
	err := json.Unmarshal(raw, &template)
	return template, err
}

// ApplyTemplate copies scoring rules onto an existing class runtime envelope.
// The returned config owns the template's map and slices just as a normal JSON
// decode would; callers serialize it before making further mutations.
func ApplyTemplate(runtime Config, template Template) Config {
	runtime.SchemeName = template.SchemeName
	runtime.Weights = template.Weights
	runtime.Categories = template.Categories
	return runtime
}

// ValidateTemplate validates only reusable scoring data by supplying a known
// valid runtime envelope. This keeps template acceptance independent of an
// arbitrary class or academic year's dates and switches.
func ValidateTemplate(template Template) error {
	return Validate(ApplyTemplate(DefaultSelfReportConfig("template"), template))
}
