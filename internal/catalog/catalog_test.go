package catalog

import (
	"testing"

	"github.com/lucasbrito-wdt/mcpgw/internal/upstream"
)

func ptr(b bool) *bool { return &b }

func TestClassify(t *testing.T) {
	readOnly := upstream.Annotations{ReadOnlyHint: true}
	tests := []struct {
		name    string
		tool    ToolDef
		trusted bool
		want    Level
	}{
		{"destructive hint", ToolDef{Name: "get_x", Annotations: upstream.Annotations{DestructiveHint: ptr(true)}}, false, LevelDestructive},
		{"destructive hint trusted", ToolDef{Name: "get_x", Annotations: upstream.Annotations{DestructiveHint: ptr(true)}}, true, LevelDestructive},
		{"read only hint untrusted is ignored", ToolDef{Name: "do_thing", Annotations: readOnly}, false, LevelWrite},
		{"read only hint trusted lowers", ToolDef{Name: "do_thing", Annotations: readOnly}, true, LevelRead},
		{"read only hint trusted lowers destructive name", ToolDef{Name: "remove_from_view", Annotations: readOnly}, true, LevelRead},
		{"read only hint untrusted keeps destructive name", ToolDef{Name: "delete_item", Annotations: readOnly}, false, LevelDestructive},
		{"destructive beats read only", ToolDef{Name: "x", Annotations: upstream.Annotations{ReadOnlyHint: true, DestructiveHint: ptr(true)}}, true, LevelDestructive},
		{"destructive hint raises read name", ToolDef{Name: "list_and_wipe", Annotations: upstream.Annotations{DestructiveHint: ptr(true)}}, false, LevelDestructive},
		{"destructive false falls to name", ToolDef{Name: "list_items", Annotations: upstream.Annotations{DestructiveHint: ptr(false)}}, false, LevelRead},
		{"destructive false never lowers name", ToolDef{Name: "delete_item", Annotations: upstream.Annotations{DestructiveHint: ptr(false)}}, false, LevelDestructive},
		// server-everything marks side-effect tools readOnlyHint:true (compat corpus).
		{"everything trigger-list-changed untrusted", ToolDef{Name: "trigger-list-changed", Annotations: readOnly}, false, LevelWrite},
		{"everything trigger-log-messages untrusted", ToolDef{Name: "trigger-log-messages", Annotations: readOnly}, false, LevelWrite},
		{"everything trigger-list-changed trusted", ToolDef{Name: "trigger-list-changed", Annotations: readOnly}, true, LevelRead},
		{"delete", ToolDef{Name: "delete_item"}, false, LevelDestructive},
		{"remove", ToolDef{Name: "remove-member"}, false, LevelDestructive},
		{"drop camel", ToolDef{Name: "dropTable"}, false, LevelDestructive},
		{"destroy", ToolDef{Name: "destroy_vm"}, false, LevelDestructive},
		{"purge", ToolDef{Name: "purge_cache"}, false, LevelDestructive},
		{"revoke", ToolDef{Name: "revoke_token"}, false, LevelDestructive},
		{"destructive verb later", ToolDef{Name: "get_and_delete"}, false, LevelDestructive},
		{"get", ToolDef{Name: "get_item"}, false, LevelRead},
		{"list", ToolDef{Name: "list_repos"}, false, LevelRead},
		{"search", ToolDef{Name: "search"}, false, LevelRead},
		{"read camel", ToolDef{Name: "readFile"}, false, LevelRead},
		{"fetch", ToolDef{Name: "fetch_url"}, false, LevelRead},
		{"query", ToolDef{Name: "query_db"}, false, LevelRead},
		{"describe", ToolDef{Name: "describe-table"}, false, LevelRead},
		{"read verb not first is not read", ToolDef{Name: "mark_read"}, false, LevelWrite},
		{"create", ToolDef{Name: "create_issue"}, false, LevelWrite},
		{"unknown", ToolDef{Name: "frobnicate"}, false, LevelWrite},
		{"unknown trusted without hint", ToolDef{Name: "frobnicate"}, true, LevelWrite},
		{"empty", ToolDef{Name: ""}, false, LevelWrite},
		{"prefix is not a word", ToolDef{Name: "getaway"}, false, LevelWrite},
		{"undelete is not delete", ToolDef{Name: "undelete_item"}, false, LevelWrite},
		{"write verb after read verb", ToolDef{Name: "search_and_replace"}, false, LevelWrite},
		{"get or create", ToolDef{Name: "get_or_create_user"}, false, LevelWrite},
		{"bare query", ToolDef{Name: "query"}, false, LevelWrite},
		{"bare fetch", ToolDef{Name: "fetch"}, false, LevelWrite},
		{"list and send", ToolDef{Name: "list_and_send_report"}, false, LevelWrite},
		{"read and update", ToolDef{Name: "read_and_update_file"}, false, LevelWrite},
		{"camel write verb", ToolDef{Name: "getAndSetValue"}, false, LevelWrite},
		{"read then destructive", ToolDef{Name: "list_and_truncate"}, false, LevelDestructive},
		{"erase", ToolDef{Name: "erase_disk"}, false, LevelDestructive},
		{"wipe", ToolDef{Name: "wipe"}, false, LevelDestructive},
		{"kill", ToolDef{Name: "kill_process"}, false, LevelDestructive},
		{"terminate", ToolDef{Name: "terminate_instance"}, false, LevelDestructive},
		{"reset", ToolDef{Name: "get_reset_link"}, false, LevelDestructive},
		{"overwrite", ToolDef{Name: "overwrite_file"}, false, LevelDestructive},
		{"truncate", ToolDef{Name: "truncate_table"}, false, LevelDestructive},
		{"read only hint trusted lowers write name", ToolDef{Name: "search_and_replace", Annotations: readOnly}, true, LevelRead},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.tool, tc.trusted); got != tc.want {
				t.Errorf("Classify(%q, trusted=%v) = %s, want %s", tc.tool.Name, tc.trusted, got, tc.want)
			}
		})
	}
}

func TestLevelRank(t *testing.T) {
	if !(LevelRead.Rank() < LevelWrite.Rank() && LevelWrite.Rank() < LevelDestructive.Rank()) {
		t.Fatal("ordering broken")
	}
	if Level("bogus").Rank() != LevelDestructive.Rank() {
		t.Fatal("unknown level must rank most restrictive")
	}
}
