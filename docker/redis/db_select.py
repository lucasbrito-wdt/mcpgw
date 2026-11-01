"""Entry point for mcp/redis that adds an optional `db` argument to every tool.

Without `db` a tool uses REDIS_DB (default 0); with `db` it runs against that logical database,
so one connection covers every database of the server.
"""

import contextvars
import functools
import inspect
import sys
from typing import Optional

sys.path.insert(0, "/app")

import redis  # noqa: E402
from mcp.server.fastmcp import FastMCP  # noqa: E402

_db: contextvars.ContextVar[Optional[int]] = contextvars.ContextVar("redis_db", default=None)
_DB_DOC = "\n\n    db (int, optional): Redis logical database for this call. Omit to use the connection default."
_original_tool = FastMCP.tool


def _tool(self, *args, **kwargs):
    register = _original_tool(self, *args, **kwargs)

    def wrap(fn):
        @functools.wraps(fn)
        async def with_db(*a, db: Optional[int] = None, **kw):
            if db is not None and db < 0:
                raise ValueError("db must be >= 0")
            token = _db.set(db)
            try:
                return await fn(*a, **kw)
            finally:
                _db.reset(token)

        sig = inspect.signature(fn)
        extra = inspect.Parameter("db", inspect.Parameter.KEYWORD_ONLY, default=None, annotation=Optional[int])
        with_db.__signature__ = sig.replace(parameters=[*sig.parameters.values(), extra])
        with_db.__annotations__ = {**fn.__annotations__, "db": Optional[int]}
        with_db.__doc__ = (fn.__doc__ or "") + _DB_DOC
        return register(with_db)

    return wrap


FastMCP.tool = _tool

from src.common.connection import RedisConnectionManager  # noqa: E402

_default_connection = RedisConnectionManager.get_connection.__func__
_clients: dict[int, redis.Redis] = {}


def _get_connection(cls, decode_responses=True):
    base = _default_connection(cls, decode_responses)
    db = _db.get()
    if db is None or not isinstance(base, redis.Redis):
        return base
    if db not in _clients:
        pool = base.connection_pool
        _clients[db] = redis.Redis(
            connection_pool=redis.ConnectionPool(
                connection_class=pool.connection_class,
                max_connections=10,
                **{**pool.connection_kwargs, "db": db},
            )
        )
    return _clients[db]


RedisConnectionManager.get_connection = classmethod(_get_connection)

from src.main import main  # noqa: E402

if __name__ == "__main__":
    main()
