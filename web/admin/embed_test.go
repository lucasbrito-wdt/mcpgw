package adminweb

import (
	"io/fs"
	"testing"
)

func TestFSIsRootedAtOut(t *testing.T) {
	if _, err := fs.Stat(FS, ".keep"); err != nil {
		// After `make admin` .keep is recreated; admin/index.html exists too.
		t.Fatalf("embedded FS not rooted at out/: %v", err)
	}
}
