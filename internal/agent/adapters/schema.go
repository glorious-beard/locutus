package adapters

// schemaIsFullyRequired reports whether every property in every
// object node appears in the node's `required` list. OpenAI's
// strict json_schema mode demands this; Anthropic and Gemini do
// not. The OpenAI Responses adapter uses this gate to decide
// whether to set Strict:true on the json_schema config — schemas
// derived from Go structs with `,omitempty` fields land here with
// a partial required list and would force the model to fabricate
// optional values under strict.
func schemaIsFullyRequired(node map[string]any) bool {
	if node == nil {
		return true
	}
	if t, ok := node["type"].(string); ok && t == "object" {
		props, _ := node["properties"].(map[string]any)
		if len(props) > 0 {
			required, _ := node["required"].([]any)
			if len(required) != len(props) {
				return false
			}
			seen := make(map[string]bool, len(required))
			for _, r := range required {
				if s, ok := r.(string); ok {
					seen[s] = true
				}
			}
			for k := range props {
				if !seen[k] {
					return false
				}
			}
			for _, v := range props {
				if child, ok := v.(map[string]any); ok {
					if !schemaIsFullyRequired(child) {
						return false
					}
				}
			}
		}
	}
	if items, ok := node["items"].(map[string]any); ok {
		if !schemaIsFullyRequired(items) {
			return false
		}
	}
	for _, key := range []string{"oneOf", "anyOf", "allOf"} {
		if list, ok := node[key].([]any); ok {
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					if !schemaIsFullyRequired(m) {
						return false
					}
				}
			}
		}
	}
	return true
}
