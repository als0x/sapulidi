package core

// Names that packs may refer to. Each category package registers its own names in init().
var (
	toolActions = map[string]map[string]bool{} // tool id → action ids, e.g. "git" → {"worktree-prune"}
	strategies  = map[string]bool{}
	locations   = map[string]bool{}
)

func RegisterToolAction(tool, action string) {
	if toolActions[tool] == nil {
		toolActions[tool] = map[string]bool{}
	}
	toolActions[tool][action] = true
}

func RegisterStrategy(id string) { strategies[id] = true }
func RegisterLocation(id string) { locations[id] = true }

func ToolActionKnown(tool, action string) bool { return toolActions[tool][action] }
func ToolKnown(tool string) bool               { return len(toolActions[tool]) > 0 }
func StrategyKnown(id string) bool             { return strategies[id] }
func LocationKnown(id string) bool             { return locations[id] }

func CategoryBucket(name string) (Bucket, bool) {
	for _, c := range categories {
		if c.Name() == name {
			return c.Bucket(), true
		}
	}
	return "", false
}
