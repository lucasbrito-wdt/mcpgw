(() => {
  "use strict";

  // Capture the host functions, then remove every ambient capability that the
  // qjs wasm build installs on globalThis (std, os, bjson, timers).
  const hostCall = globalThis.__call;
  const hostSettle = globalThis.__settle;
  for (const name of [
    "__call", "__settle", "std", "os", "bjson",
    "setTimeout", "setInterval", "clearTimeout", "clearInterval",
  ]) {
    delete globalThis[name];
  }

  // Defense in depth: the qjs: modules stay registered in the engine and cannot be
  // unregistered, so close the paths that could reach them from script text.
  // import(...) is rejected by a source scan in Go; these close string-to-code.
  const blocked = function () {
    throw new TypeError("code generation from strings is disabled");
  };
  delete globalThis.eval;
  Object.defineProperty(globalThis, "Function", { value: blocked });
  for (const proto of [
    Object.getPrototypeOf(function () {}),
    Object.getPrototypeOf(async function () {}),
    Object.getPrototypeOf(function* () {}),
    Object.getPrototypeOf(async function* () {}),
  ]) {
    Object.defineProperty(proto, "constructor", { value: blocked });
  }

  const stringify = JSON.stringify;
  const parse = JSON.parse;

  // The promise handed to the script is marked as handled here so an
  // un-awaited call that fails never raises an unhandled rejection; a script
  // that awaits it still sees the rejection.
  const thenOf = Promise.prototype.then;
  const noop = () => {};
  const call = (name, args) => {
    const promise = (async () =>
      parse(await hostCall(String(name), stringify(args === undefined ? null : args))))();
    thenOf.call(promise, undefined, noop);
    return promise;
  };

  // Convenience: tools.<server>.<tool>(args) === tools.call("<server>.<tool>", args).
  // Only well-formed names resolve ("then" is excluded so a proxy is never
  // mistaken for a thenable), everything else is undefined.
  const serverPattern = /^[a-z][a-z0-9_]{0,62}$/;
  const toolPattern = /^[A-Za-z0-9_.-]{1,128}$/;
  const test = (pattern, key) => typeof key === "string" && key !== "then" && RegExp.prototype.test.call(pattern, key);
  const readOnly = {
    set: () => false,
    defineProperty: () => false,
    deleteProperty: () => false,
    setPrototypeOf: () => false,
  };
  const serverProxy = (server) =>
    new Proxy(Object.freeze({}), {
      ...readOnly,
      get: (_target, key) =>
        test(toolPattern, key) ? Object.freeze((args) => call(server + "." + key, args)) : undefined,
    });
  const toolsProxy = new Proxy(Object.freeze({ call }), {
    ...readOnly,
    get: (target, key) => {
      if (key === "call") return target.call;
      return test(serverPattern, key) ? serverProxy(key) : undefined;
    },
  });
  Object.defineProperty(globalThis, "tools", { value: toolsProxy, enumerable: false });

  const describe = (err) => {
    if (err instanceof Error) return typeof err.stack === "string" ? `${err.name}: ${err.message}\n${err.stack}` : `${err.name}: ${err.message}`;
    try {
      const json = stringify(err);
      if (json !== undefined) return json;
    } catch (_) { /* fall through */ }
    return String(err);
  };

  const serialize = (value) => {
    if (value === undefined) return "null";
    const type = typeof value;
    if (type === "function" || type === "symbol" || type === "bigint") {
      throw new TypeError(`${type} values cannot be returned`);
    }
    const json = stringify(value);
    return json === undefined ? "null" : json;
  };

  const run = async (userFn) => {
    if (typeof userFn !== "function") {
      throw new TypeError("code must evaluate to a function, e.g. async () => { ... }");
    }
    return await userFn();
  };

  return (userFn) => {
    run(userFn).then(
      (value) => {
        let json;
        try {
          json = serialize(value);
        } catch (err) {
          hostSettle("unserializable", String(err && err.message));
          return;
        }
        hostSettle("ok", json);
      },
      (err) => hostSettle("error", describe(err)),
    );
  };
})()
