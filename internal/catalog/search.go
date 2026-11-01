package catalog

import (
	"sort"
	"strings"
)

// Search ranks tools by simple keyword match over qualified name, name words and description.
// Every query token that matches adds to the score; tools matching nothing are dropped.
// An empty query returns all tools sorted by qualified name. The input slice is not modified.
// Callers must apply policy filtering before calling Search.
func Search(tools []ToolDef, query string) []ToolDef {
	tokens := strings.Fields(strings.ToLower(query))
	type scored struct {
		tool  ToolDef
		score int
		key   string
	}
	var out []scored
	for _, t := range tools {
		s := scored{tool: t, key: QualifiedName(t.Server, t.Name)}
		if len(tokens) == 0 {
			out = append(out, s)
			continue
		}
		s.score = score(t, tokens)
		if s.score > 0 {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].score != out[j].score {
			return out[i].score > out[j].score
		}
		return out[i].key < out[j].key
	})
	res := make([]ToolDef, len(out))
	for i, s := range out {
		res[i] = s.tool
	}
	return res
}

func score(t ToolDef, tokens []string) int {
	name := strings.ToLower(t.Name)
	words := nameWords(t.Name)
	server := strings.ToLower(t.Server)
	desc := strings.ToLower(t.Description)
	total := 0
	for _, tok := range tokens {
		switch {
		case name == tok:
			total += 10
		case containsWord(words, tok):
			total += 6
		case strings.Contains(name, tok):
			total += 4
		}
		if server == tok {
			total += 3
		} else if strings.Contains(server, tok) {
			total++
		}
		if strings.Contains(desc, tok) {
			total += 2
		}
	}
	return total
}

func containsWord(words []string, tok string) bool {
	for _, w := range words {
		if w == tok {
			return true
		}
	}
	return false
}
