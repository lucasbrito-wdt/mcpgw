package sandbox

import "github.com/fastschema/qjs"

// releaseWazeroRuntime frees the wasm instance behind rt without running guest code, so it is
// safe after a trap or deadline. Every qjs.Runtime shares one wazero runtime (third_party/qjs),
// so this closes the instance and its proxy entry, never the shared runtime. Without it a killed
// instance stays reachable through the proxy registry: TestFinishedAndKilledInstancesDoNotRetainMemory.
func releaseWazeroRuntime(rt *qjs.Runtime) {
	defer func() { _ = recover() }()
	rt.Release()
}
