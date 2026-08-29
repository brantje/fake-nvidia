#ifndef FAKE_NVIDIA_CUDA_ABI_H
#define FAKE_NVIDIA_CUDA_ABI_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

typedef int CUresult;
typedef int CUdevice;
typedef uintptr_t CUdeviceptr;
typedef void *CUcontext;
typedef void *CUmodule;
typedef void *CUfunction;
typedef void *CUstream;
typedef int CUdevice_attribute;
typedef int CUjit_option;
typedef unsigned long long cuuint64_t;
typedef int CUdriverProcAddressQueryResult;

typedef struct {
    unsigned char bytes[16];
} CUuuid;

typedef int cudaError_t;
typedef void *cudaStream_t;
typedef struct {
    unsigned int x;
    unsigned int y;
    unsigned int z;
} dim3;

typedef struct {
    unsigned char bytes[16];
} cudaUUID_t;

typedef struct {
    char name[256];
    cudaUUID_t uuid;
    char luid[8];
    unsigned int luidDeviceNodeMask;
    size_t totalGlobalMem;
    size_t sharedMemPerBlock;
    int regsPerBlock;
    int warpSize;
    size_t memPitch;
    int maxThreadsPerBlock;
    int maxThreadsDim[3];
    int maxGridSize[3];
    int clockRate;
    size_t totalConstMem;
    int major;
    int minor;
} fakeCudaDevicePropPrefix;

enum {
    FAKE_CUDA_SUCCESS = 0,
    FAKE_CUDA_ERROR_INVALID_VALUE = 1,
    FAKE_CUDA_ERROR_MEMORY_ALLOCATION = 2,
    FAKE_CUDA_ERROR_INITIALIZATION = 3,
    FAKE_CUDA_ERROR_INVALID_DEVICE = 10,
    FAKE_CUDA_ERROR_NO_DEVICE = 100,
    FAKE_CUDA_ERROR_NOT_SUPPORTED = 801,
    FAKE_CUDA_ERROR_UNKNOWN = 999
};

enum {
    FAKE_CUDA_DRIVER_SUCCESS = 0,
    FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE = 1,
    FAKE_CUDA_DRIVER_ERROR_OUT_OF_MEMORY = 2,
    FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED = 3,
    FAKE_CUDA_DRIVER_ERROR_NO_DEVICE = 100,
    FAKE_CUDA_DRIVER_ERROR_INVALID_DEVICE = 101,
    FAKE_CUDA_DRIVER_ERROR_NOT_FOUND = 500,
    FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED = 801,
    FAKE_CUDA_DRIVER_ERROR_UNKNOWN = 999
};

void *fake_cuda_alloc_token(void);
void fake_cuda_free_token(void *ptr);
uintptr_t fake_cuda_token_value(void *ptr);
void *fake_cuda_token_pointer(uintptr_t value);
void fake_cuda_copy_string(char *dst, int len, const char *src);
int fake_cuda_parse_uuid(const char *text, unsigned char out[16]);
void fake_cuda_fill_properties(void *dst, const char *name, const char *uuid,
                               size_t total_bytes, int major, int minor);
void fake_cuda_memcpy_host(void *dst, const void *src, size_t count);
const char *fake_cuda_driver_error_name(int code);
const char *fake_cuda_driver_error_string(int code);
const char *fake_cuda_runtime_error_name(int code);
const char *fake_cuda_runtime_error_string(int code);
void *fake_cuda_lookup_symbol(const char *symbol);

#ifdef __cplusplus
}
#endif

#endif
