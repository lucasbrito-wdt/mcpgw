// Package deps pins phase-1 dependencies with blank imports so that
// `go mod tidy` keeps them in go.mod before the packages that use them exist.
// Delete an import here once a real package imports the same module.
package deps

import (
	_ "github.com/tetratelabs/wazero"
)
