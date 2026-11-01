package sandbox

import (
	"context"
	"reflect"
	"unsafe"

	"github.com/fastschema/qjs"
	"github.com/tetratelabs/wazero"
)

// releaseWazeroRuntime closes the wazero runtime that qjs.Runtime creates in New
// and never closes (its unexported wrt field, qjs v0.0.6). Root cause of the ~0.5 MB
// retained per execution: the runtime keeps its Store, and the Store its MemoryInstance
// buffers, reachable until the runtime is closed, for finished and killed instances
// alike. Closing it touches no guest code, so it is safe after a trap or deadline.
// TestFinishedAndKilledInstancesDoNotRetainMemory fails if the field is renamed.
func releaseWazeroRuntime(rt *qjs.Runtime) {
	defer func() { _ = recover() }()
	f := reflect.ValueOf(rt).Elem().FieldByName("wrt")
	if !f.IsValid() {
		return
	}
	wrt, ok := reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Interface().(wazero.Runtime)
	if !ok || wrt == nil {
		return
	}
	_ = wrt.Close(context.Background())
}
