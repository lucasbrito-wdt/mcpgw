package catalog

import "testing"

func TestSearch(t *testing.T) {
	tools := []ToolDef{
		{Server: "github", Name: "create_issue", Description: "Create a new issue in a repository"},
		{Server: "github", Name: "list_issues", Description: "List issues of a repository"},
		{Server: "slack", Name: "post_message", Description: "Post a message to a channel"},
		{Server: "github", Name: "issue", Description: "Exact name"},
	}
	names := func(ts []ToolDef) []string {
		var out []string
		for _, t := range ts {
			out = append(out, QualifiedName(t.Server, t.Name))
		}
		return out
	}
	eq := func(got, want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v want %v", got, want)
		}
		for i := range got {
			if got[i] != want[i] {
				t.Fatalf("got %v want %v", got, want)
			}
		}
	}
	eq(names(Search(tools, "issue")), []string{"github.issue", "github.create_issue", "github.list_issues"})
	eq(names(Search(tools, "slack message")), []string{"slack.post_message"})
	eq(names(Search(tools, "REPOSITORY list")), []string{"github.list_issues", "github.create_issue"})
	eq(names(Search(tools, "nothing-matches")), nil)
	eq(names(Search(tools, "")), []string{"github.create_issue", "github.issue", "github.list_issues", "slack.post_message"})
	if tools[0].Name != "create_issue" {
		t.Fatal("input mutated")
	}
}
