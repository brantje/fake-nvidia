#define _GNU_SOURCE
#include "cuda_abi.h"

#include <ctype.h>
#include <dlfcn.h>
#include <stdlib.h>
#include <string.h>

void *fake_cuda_alloc_token(void) {
    return malloc(1);
}

void fake_cuda_free_token(void *ptr) {
    free(ptr);
}

uintptr_t fake_cuda_token_value(void *ptr) {
    return (uintptr_t)ptr;
}

void *fake_cuda_token_pointer(uintptr_t value) {
    return (void *)value;
}

void fake_cuda_copy_string(char *dst, int len, const char *src) {
    if (dst == NULL || len <= 0) {
        return;
    }
    if (src == NULL) {
        dst[0] = '\0';
        return;
    }
    size_t n = strlen(src);
    if (n >= (size_t)len) {
        n = (size_t)len - 1;
    }
    memcpy(dst, src, n);
    dst[n] = '\0';
}

static int hex_value(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    c = (char)tolower((unsigned char)c);
    if (c >= 'a' && c <= 'f') return 10 + c - 'a';
    return -1;
}

int fake_cuda_parse_uuid(const char *text, unsigned char out[16]) {
    if (text == NULL || out == NULL) return 0;
    if (strncmp(text, "GPU-", 4) == 0) text += 4;
    char hex[33];
    size_t count = 0;
    for (; *text != '\0' && count < 32; ++text) {
        if (*text == '-') continue;
        if (hex_value(*text) < 0) return 0;
        hex[count++] = *text;
    }
    if (count != 32) return 0;
    hex[32] = '\0';
    for (size_t i = 0; i < 16; ++i) {
        int hi = hex_value(hex[i * 2]);
        int lo = hex_value(hex[i * 2 + 1]);
        if (hi < 0 || lo < 0) return 0;
        out[i] = (unsigned char)((hi << 4) | lo);
    }
    return 1;
}

void fake_cuda_fill_properties(void *dst, const char *name, const char *uuid,
                               size_t total_bytes, int major, int minor) {
    if (dst == NULL) return;
    fakeCudaDevicePropPrefix *prop = (fakeCudaDevicePropPrefix *)dst;
    memset(prop, 0, sizeof(*prop));
    fake_cuda_copy_string(prop->name, (int)sizeof(prop->name), name);
    (void)fake_cuda_parse_uuid(uuid, prop->uuid.bytes);
    prop->totalGlobalMem = total_bytes;
    prop->sharedMemPerBlock = 48 * 1024;
    prop->regsPerBlock = 65536;
    prop->warpSize = 32;
    prop->memPitch = (size_t)2147483647;
    prop->maxThreadsPerBlock = 1024;
    prop->maxThreadsDim[0] = 1024;
    prop->maxThreadsDim[1] = 1024;
    prop->maxThreadsDim[2] = 64;
    prop->maxGridSize[0] = 2147483647;
    prop->maxGridSize[1] = 65535;
    prop->maxGridSize[2] = 65535;
    prop->totalConstMem = 64 * 1024;
    prop->major = major;
    prop->minor = minor;
}

void fake_cuda_memcpy_host(void *dst, const void *src, size_t count) {
    if (count != 0) memcpy(dst, src, count);
}

const char *fake_cuda_driver_error_name(int code) {
    switch (code) {
    case 0: return "CUDA_SUCCESS";
    case 1: return "CUDA_ERROR_INVALID_VALUE";
    case 2: return "CUDA_ERROR_OUT_OF_MEMORY";
    case 3: return "CUDA_ERROR_NOT_INITIALIZED";
    case 100: return "CUDA_ERROR_NO_DEVICE";
    case 101: return "CUDA_ERROR_INVALID_DEVICE";
    case 500: return "CUDA_ERROR_NOT_FOUND";
    case 801: return "CUDA_ERROR_NOT_SUPPORTED";
    default: return "CUDA_ERROR_UNKNOWN";
    }
}

const char *fake_cuda_driver_error_string(int code) {
    switch (code) {
    case 0: return "no error";
    case 1: return "invalid argument";
    case 2: return "out of memory";
    case 3: return "driver not initialized";
    case 100: return "no CUDA-capable device is detected";
    case 101: return "invalid device ordinal";
    case 500: return "named symbol was not found";
    case 801: return "operation is not supported by fake-nvidia";
    default: return "unknown CUDA driver error";
    }
}

const char *fake_cuda_runtime_error_name(int code) {
    switch (code) {
    case 0: return "cudaSuccess";
    case 1: return "cudaErrorInvalidValue";
    case 2: return "cudaErrorMemoryAllocation";
    case 3: return "cudaErrorInitializationError";
    case 10: return "cudaErrorInvalidDevice";
    case 100: return "cudaErrorNoDevice";
    case 801: return "cudaErrorNotSupported";
    default: return "cudaErrorUnknown";
    }
}

const char *fake_cuda_runtime_error_string(int code) {
    switch (code) {
    case 0: return "no error";
    case 1: return "invalid argument";
    case 2: return "out of memory";
    case 3: return "initialization error";
    case 10: return "invalid device ordinal";
    case 100: return "no CUDA-capable device is detected";
    case 801: return "operation is not supported by fake-nvidia";
    default: return "unknown CUDA runtime error";
    }
}

static int supported_symbol(const char *symbol) {
    static const char *const names[] = {
        "cuInit", "cuDriverGetVersion", "cuDeviceGetCount", "cuDeviceGet",
        "cuDeviceGetName", "cuDeviceGetUuid", "cuDeviceGetUuid_v2",
        "cuDeviceGetPCIBusId", "cuDeviceTotalMem", "cuDeviceTotalMem_v2",
        "cuDeviceComputeCapability", "cuDeviceGetAttribute", "cuMemGetInfo",
        "cuMemGetInfo_v2", "cuMemAlloc", "cuMemAlloc_v2", "cuMemFree",
        "cuMemFree_v2", "cuGetErrorName", "cuGetErrorString",
        "cuGetProcAddress", "cuGetProcAddress_v2", "cuModuleLoadData",
        "cuModuleLoadDataEx", "cuModuleGetFunction", "cuLaunchKernel"
    };
    if (symbol == NULL) return 0;
    for (size_t i = 0; i < sizeof(names) / sizeof(names[0]); ++i) {
        if (strcmp(symbol, names[i]) == 0) return 1;
    }
    return 0;
}

void *fake_cuda_lookup_symbol(const char *symbol) {
    if (!supported_symbol(symbol)) return NULL;
    void *handle = dlopen("libcuda.so.1", RTLD_NOW | RTLD_NOLOAD);
    if (handle == NULL) handle = dlopen(NULL, RTLD_NOW);
    if (handle == NULL) return NULL;
    return dlsym(handle, symbol);
}
