package catalog

import (
	"regexp"
	"strings"
)

var (
	destructiveWords = wordSet("delete", "remove", "drop", "destroy", "purge", "revoke",
		"erase", "wipe", "truncate", "kill", "terminate", "reset", "overwrite")
	writeWords = wordSet("send", "create", "update", "replace", "write", "post", "put", "set", "execute", "run",
		"insert", "upsert", "add", "save", "upload", "submit", "publish", "move", "rename", "copy", "edit",
		"modify", "patch", "apply", "commit", "push", "merge", "invoke", "trigger", "call")
	readWords = wordSet("get", "list", "search", "read", "fetch", "query", "describe")
	// bareReadWords are read verbs that run arbitrary upstream operations when used alone
	// (a bare "query" or "fetch" tool takes a statement or request to execute).
	bareReadWords = wordSet("query", "fetch")
	camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func wordSet(words ...string) map[string]bool {
	set := make(map[string]bool, len(words))
	for _, w := range words {
		set[w] = true
	}
	return set
}

// Classify derives a trust level from the tool name and annotations.
//
// Annotations are untrusted hints (servers mislabel side-effect tools as read-only), so they can
// only raise the level, except for servers whose annotations the admin trusts:
//   - level = max(name heuristic, destructive when destructiveHint is true);
//   - readOnlyHint lowers the level to read only when trustAnnotations is set and destructiveHint
//     is not true (a contradictory upstream is treated as dangerous);
//   - a name is read only when it starts with a read verb and no word in it is a write or
//     destructive verb; anything else, including a bare "query" or "fetch", is write.
func Classify(t ToolDef, trustAnnotations bool) Level {
	if t.Annotations.DestructiveHint != nil && *t.Annotations.DestructiveHint {
		return LevelDestructive
	}
	if trustAnnotations && t.Annotations.ReadOnlyHint {
		return LevelRead
	}
	return classifyName(t.Name)
}

func classifyName(name string) Level {
	words := nameWords(name)
	// Destructive verbs anywhere in the name beat read verbs ("get_and_delete").
	for _, w := range words {
		if destructiveWords[w] {
			return LevelDestructive
		}
	}
	// A write verb anywhere wins over a leading read verb ("search_and_replace", "get_or_create_user").
	for _, w := range words {
		if writeWords[w] {
			return LevelWrite
		}
	}
	if len(words) == 1 && bareReadWords[words[0]] {
		return LevelWrite
	}
	if len(words) > 0 && readWords[words[0]] {
		return LevelRead
	}
	return LevelWrite
}

// nameWords splits snake_case, kebab-case, dotted and camelCase names into lowercase words.
func nameWords(name string) []string {
	name = camelBoundary.ReplaceAllString(name, "${1}_${2}")
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
}
