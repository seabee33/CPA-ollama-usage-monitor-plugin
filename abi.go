package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; host_call_fn call; free_fn free_buffer; } cliproxy_host_api;
typedef int (*plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*shutdown_fn)(void);
typedef struct { uint32_t abi_version; plugin_call_fn call; free_fn free_buffer; shutdown_fn shutdown; } cliproxy_plugin_api;
extern int quotaPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void quotaPluginFree(void*, size_t);
extern void quotaPluginShutdown(void);
*/
import "C"

import "unsafe"

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if host == nil || plugin == nil || host.abi_version != 1 {
		return 1
	}
	plugin.abi_version = 1
	plugin.call = C.plugin_call_fn(C.quotaPluginCall)
	plugin.free_buffer = C.free_fn(C.quotaPluginFree)
	plugin.shutdown = C.shutdown_fn(C.quotaPluginShutdown)
	return 0
}

//export quotaPluginCall
func quotaPluginCall(method *C.char, request *C.uint8_t, length C.size_t, response *C.cliproxy_buffer) (code C.int) {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	defer func() {
		if recover() != nil {
			writeABI(response, []byte(`{"ok":false,"error":{"code":"internal_error","message":"quota plugin failed"}}`))
			code = 1
		}
	}()
	if method == nil || length > 1<<20 || (length > 0 && request == nil) {
		writeABI(response, []byte(`{"ok":false,"error":{"code":"invalid_request","message":"invalid or oversized ABI request"}}`))
		return 1
	}
	raw := C.GoBytes(unsafe.Pointer(request), C.int(length))
	result, err := app.dispatch(C.GoString(method), raw)
	if err != nil {
		return 1
	}
	if !writeABI(response, result) {
		return 1
	}
	return 0
}
func writeABI(response *C.cliproxy_buffer, raw []byte) bool {
	ptr := C.CBytes(raw)
	if ptr == nil {
		return false
	}
	response.ptr = ptr
	response.len = C.size_t(len(raw))
	return true
}

//export quotaPluginFree
func quotaPluginFree(ptr unsafe.Pointer, _ C.size_t) { C.free(ptr) }

//export quotaPluginShutdown
func quotaPluginShutdown() { app.monitor.client.CloseIdleConnections() }
