#!/usr/bin/env python3
"""Load the actual shared library and verify CPA's ABI without real credentials."""
import ctypes as c
import json
import sys


class Buffer(c.Structure):
    _fields_ = [("ptr", c.c_void_p), ("len", c.c_size_t)]


CALL = c.CFUNCTYPE(c.c_int, c.c_char_p, c.c_void_p, c.c_size_t, c.POINTER(Buffer))
FREE = c.CFUNCTYPE(None, c.c_void_p, c.c_size_t)
STOP = c.CFUNCTYPE(None)


class Host(c.Structure):
    _fields_ = [("version", c.c_uint32), ("context", c.c_void_p),
                ("call", c.c_void_p), ("free", c.c_void_p)]


class Plugin(c.Structure):
    _fields_ = [("version", c.c_uint32), ("call", CALL), ("free", FREE), ("stop", STOP)]


lib = c.CDLL(sys.argv[1])
lib.cliproxy_plugin_init.argtypes = [c.POINTER(Host), c.POINTER(Plugin)]
lib.cliproxy_plugin_init.restype = c.c_int
host, plugin = Host(1, None, None, None), Plugin()
assert lib.cliproxy_plugin_init(c.byref(host), c.byref(plugin)) == 0
assert plugin.version == 1


def invoke(method, payload):
    raw = json.dumps(payload).encode()
    buf = c.create_string_buffer(raw)
    result = Buffer()
    assert plugin.call(method.encode(), buf, len(raw), c.byref(result)) == 0
    try:
        return json.loads(c.string_at(result.ptr, result.len))
    finally:
        plugin.free(result.ptr, result.len)


registration = invoke("plugin.register", {"schema_version": 10})
assert registration["ok"]
assert registration["result"]["capabilities"] == {"management_api": True}
resources = invoke("management.register", {})["result"]["resources"]
assert resources[0]["Menu"] == "Ollama Usage"
assert not invoke("quota.fetch", {"attributes": {"base_url": "http://localhost", "api_key": "test"}})["ok"]
page = invoke("management.handle", {"Method": "GET", "Path": "/v0/resource/plugins/ollama-usage-monitor/dashboard"})
assert page["result"]["StatusCode"] == 200
assert not invoke("quota.reset", {})["ok"]
plugin.stop()
print("Native ABI smoke test passed")
