// Package route resolves client model names to provider families.
package route

// Profile describes one provider family's model routing keys.
type Profile struct {
	ID          string
	Qualifiers  []string
	ExactModels []string
	Prefixes    []string
}
