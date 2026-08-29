#define _GNU_SOURCE
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define CUDA_SUCCESS 0
#define CUDA_ERROR_OUT_OF_MEMORY 2
#define CUDA_ERROR_NOT_FOUND 500
#define CUDA_ERROR_NOT_SUPPORTED 801
#define cudaSuccess 0
#define cudaErrorMemoryAllocation 2
#define cudaErrorNotSupported 801
#define MIB (1024ULL * 1024ULL)

typedef int CUresult;
typedef int CUdevice;
typedef uintptr_t CUdeviceptr;
typedef void *CUfunction;
typedef void *CUstream;
typedef unsigned long long cuuint64_t;
typedef int cudaError_t;
typedef struct { unsigned int x, y, z; } dim3;
typedef void *cudaStream_t;
typedef struct { unsigned char bytes[16]; } cudaUUID_t;
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
} prop_prefix;

#define LOAD(handle, name) \
    name##_fn name = (name##_fn)dlsym((handle), #name); \
    if ((name) == NULL) fail("missing symbol " #name)

static void fail(const char *message) {
    fprintf(stderr, "cuda probe: %s\n", message);
    exit(2);
}

static unsigned long long visible_used_mib(void) {
    FILE *pipe = popen("nvidia-smi -i 0 --query-gpu=memory.used --format=csv,noheader,nounits", "r");
    if (pipe == NULL) fail("popen nvidia-smi");
    char line[128] = {0};
    if (fgets(line, sizeof(line), pipe) == NULL) fail("read nvidia-smi memory.used");
    int status = pclose(pipe);
    if (status != 0) fail("nvidia-smi memory.used failed");
    char *end = NULL;
    unsigned long long value = strtoull(line, &end, 10);
    if (end == line) fail("parse nvidia-smi memory.used");
    return value;
}

static void require_result(int got, int want, const char *operation) {
    if (got != want) {
        fprintf(stderr, "cuda probe: %s result=%d want=%d\n", operation, got, want);
        exit(2);
    }
}

typedef CUresult (*cuInit_fn)(unsigned int);
typedef CUresult (*cuDriverGetVersion_fn)(int *);
typedef CUresult (*cuDeviceGetCount_fn)(int *);
typedef CUresult (*cuDeviceGet_fn)(CUdevice *, int);
typedef CUresult (*cuDeviceGetName_fn)(char *, int, CUdevice);
typedef CUresult (*cuDeviceGetUuid_fn)(void *, CUdevice);
typedef CUresult (*cuDeviceGetPCIBusId_fn)(char *, int, CUdevice);
typedef CUresult (*cuDeviceTotalMem_v2_fn)(size_t *, CUdevice);
typedef CUresult (*cuDeviceComputeCapability_fn)(int *, int *, CUdevice);
typedef CUresult (*cuMemGetInfo_v2_fn)(size_t *, size_t *);
typedef CUresult (*cuMemAlloc_v2_fn)(CUdeviceptr *, size_t);
typedef CUresult (*cuMemFree_v2_fn)(CUdeviceptr);
typedef CUresult (*cuGetProcAddress_fn)(const char *, void **, int, cuuint64_t);
typedef CUresult (*cuLaunchKernel_fn)(CUfunction, unsigned int, unsigned int, unsigned int, unsigned int, unsigned int, unsigned int, unsigned int, CUstream, void **, void **);

typedef cudaError_t (*cudaDriverGetVersion_fn)(int *);
typedef cudaError_t (*cudaGetDeviceCount_fn)(int *);
typedef cudaError_t (*cudaSetDevice_fn)(int);
typedef cudaError_t (*cudaGetDevice_fn)(int *);
typedef cudaError_t (*cudaMemGetInfo_fn)(size_t *, size_t *);
typedef cudaError_t (*cudaMalloc_fn)(void **, size_t);
typedef cudaError_t (*cudaFree_fn)(void *);
typedef cudaError_t (*cudaGetDeviceProperties_fn)(void *, int);
typedef cudaError_t (*cudaMemcpy_fn)(void *, const void *, size_t, int);
typedef cudaError_t (*cudaLaunchKernel_fn)(const void *, dim3, dim3, void **, size_t, cudaStream_t);
typedef const char *(*cudaGetErrorString_fn)(cudaError_t);

int main(int argc, char **argv) {
    if (argc != 6) fail("usage: cuda_probe <basic|oom> <count> <total0> <major0> <minor0>");
    int expected_count = atoi(argv[2]);
    size_t expected_total = (size_t)strtoull(argv[3], NULL, 10);
    int expected_major = atoi(argv[4]);
    int expected_minor = atoi(argv[5]);

    void *driver = dlopen("libcuda.so.1", RTLD_NOW | RTLD_LOCAL);
    if (driver == NULL) fail(dlerror());
    void *runtime = dlopen("libcudart.so", RTLD_NOW | RTLD_LOCAL);
    if (runtime == NULL) fail(dlerror());

    LOAD(driver, cuInit);
    LOAD(driver, cuDriverGetVersion);
    LOAD(driver, cuDeviceGetCount);
    LOAD(driver, cuDeviceGet);
    LOAD(driver, cuDeviceGetName);
    LOAD(driver, cuDeviceGetUuid);
    LOAD(driver, cuDeviceGetPCIBusId);
    LOAD(driver, cuDeviceTotalMem_v2);
    LOAD(driver, cuDeviceComputeCapability);
    LOAD(driver, cuMemGetInfo_v2);
    LOAD(driver, cuMemAlloc_v2);
    LOAD(driver, cuMemFree_v2);
    LOAD(driver, cuGetProcAddress);
    LOAD(driver, cuLaunchKernel);

    LOAD(runtime, cudaDriverGetVersion);
    LOAD(runtime, cudaGetDeviceCount);
    LOAD(runtime, cudaSetDevice);
    LOAD(runtime, cudaGetDevice);
    LOAD(runtime, cudaMemGetInfo);
    LOAD(runtime, cudaMalloc);
    LOAD(runtime, cudaFree);
    LOAD(runtime, cudaGetDeviceProperties);
    LOAD(runtime, cudaMemcpy);
    LOAD(runtime, cudaLaunchKernel);
    LOAD(runtime, cudaGetErrorString);

    require_result(cuInit(0), CUDA_SUCCESS, "cuInit");
    int driver_version = 0;
    require_result(cuDriverGetVersion(&driver_version), CUDA_SUCCESS, "cuDriverGetVersion");
    if (driver_version != 12080) fail("driver version does not follow configured CUDA version");

    int count = -1;
    require_result(cuDeviceGetCount(&count), CUDA_SUCCESS, "cuDeviceGetCount");
    if (count != expected_count) fail("driver device count mismatch");
    count = -1;
    require_result(cudaGetDeviceCount(&count), cudaSuccess, "cudaGetDeviceCount");
    if (count != expected_count) fail("runtime device count mismatch");

    CUdevice dev = -1;
    require_result(cuDeviceGet(&dev, 0), CUDA_SUCCESS, "cuDeviceGet");
    char name[256] = {0};
    require_result(cuDeviceGetName(name, sizeof(name), dev), CUDA_SUCCESS, "cuDeviceGetName");
    if (name[0] == '\0') fail("empty device name");
    unsigned char uuid[16] = {0};
    require_result(cuDeviceGetUuid(uuid, dev), CUDA_SUCCESS, "cuDeviceGetUuid");
    int uuid_nonzero = 0;
    for (size_t i = 0; i < sizeof(uuid); ++i) uuid_nonzero |= uuid[i] != 0;
    if (!uuid_nonzero) fail("empty device UUID");
    char pci[32] = {0};
    require_result(cuDeviceGetPCIBusId(pci, sizeof(pci), dev), CUDA_SUCCESS, "cuDeviceGetPCIBusId");
    if (pci[0] == '\0') fail("empty PCI bus id");

    size_t total = 0;
    require_result(cuDeviceTotalMem_v2(&total, dev), CUDA_SUCCESS, "cuDeviceTotalMem_v2");
    if (total != expected_total) fail("driver total memory mismatch");
    int major = 0, minor = 0;
    require_result(cuDeviceComputeCapability(&major, &minor, dev), CUDA_SUCCESS, "cuDeviceComputeCapability");
    if (major != expected_major || minor != expected_minor) fail("compute capability mismatch");

    prop_prefix prop;
    memset(&prop, 0xaa, sizeof(prop));
    require_result(cudaGetDeviceProperties(&prop, 0), cudaSuccess, "cudaGetDeviceProperties");
    if (prop.totalGlobalMem != expected_total || prop.major != expected_major || prop.minor != expected_minor || prop.name[0] == '\0') {
        fail("runtime device properties mismatch");
    }

    if (expected_count > 1) {
        require_result(cudaSetDevice(1), cudaSuccess, "cudaSetDevice(1)");
        int current = -1;
        require_result(cudaGetDevice(&current), cudaSuccess, "cudaGetDevice");
        if (current != 1) fail("current device mismatch");
        require_result(cudaSetDevice(0), cudaSuccess, "cudaSetDevice(0)");
    }

    size_t free_before = 0, runtime_total = 0;
    require_result(cudaMemGetInfo(&free_before, &runtime_total), cudaSuccess, "cudaMemGetInfo");
    if (runtime_total != expected_total) fail("runtime total memory mismatch");
    size_t driver_free = 0, driver_total = 0;
    require_result(cuMemGetInfo_v2(&driver_free, &driver_total), CUDA_SUCCESS, "cuMemGetInfo_v2");
    if (driver_free != free_before || driver_total != runtime_total) fail("driver/runtime memory info mismatch");

    unsigned long long used_before = visible_used_mib();
    if (strcmp(argv[1], "oom") == 0) {
        void *ptr = NULL;
        require_result(cudaMalloc(&ptr, 1 * MIB), cudaErrorMemoryAllocation, "forced cudaMalloc OOM");
        if (ptr != NULL) fail("OOM allocation returned a pointer");
        if (visible_used_mib() != used_before) fail("forced OOM changed NVML used memory");
        puts("oom-ok");
        return 0;
    }
    if (strcmp(argv[1], "basic") != 0) fail("unknown mode");

    void *ptr = NULL;
    require_result(cudaMalloc(&ptr, 64 * MIB), cudaSuccess, "cudaMalloc");
    if (ptr == NULL) fail("cudaMalloc returned NULL");
    if (visible_used_mib() != used_before + 64) fail("cudaMalloc not visible through nvidia-smi");
    size_t free_after = 0, total_after = 0;
    require_result(cudaMemGetInfo(&free_after, &total_after), cudaSuccess, "cudaMemGetInfo after malloc");
    if (free_after + 64 * MIB != free_before) fail("cudaMalloc did not reduce free memory");
    require_result(cudaFree(ptr), cudaSuccess, "cudaFree");
    if (visible_used_mib() != used_before) fail("cudaFree not visible through nvidia-smi");

    CUdeviceptr driver_ptr = 0;
    require_result(cuMemAlloc_v2(&driver_ptr, 32 * MIB), CUDA_SUCCESS, "cuMemAlloc_v2");
    if (driver_ptr == 0) fail("cuMemAlloc_v2 returned zero token");
    if (visible_used_mib() != used_before + 32) fail("cuMemAlloc_v2 not visible through nvidia-smi");
    require_result(cuMemFree_v2(driver_ptr), CUDA_SUCCESS, "cuMemFree_v2");
    if (visible_used_mib() != used_before) fail("cuMemFree_v2 not visible through nvidia-smi");

    void *too_large = NULL;
    require_result(cudaMalloc(&too_large, free_before + 1), cudaErrorMemoryAllocation, "capacity cudaMalloc OOM");
    if (too_large != NULL) fail("capacity OOM returned a pointer");
    if (visible_used_mib() != used_before) fail("capacity OOM changed NVML used memory");

    char src[] = "cpu-copy";
    char dst[sizeof(src)];
    memset(dst, 0, sizeof(dst));
    require_result(cudaMemcpy(dst, src, sizeof(src), 0), cudaSuccess, "cudaMemcpy host-to-host");
    if (memcmp(src, dst, sizeof(src)) != 0) fail("host-to-host cudaMemcpy did not copy");
    require_result(cudaMemcpy(dst, src, sizeof(src), 1), cudaErrorNotSupported, "cudaMemcpy host-to-device");

    void *resolved = NULL;
    require_result(cuGetProcAddress("cuMemAlloc_v2", &resolved, 12080, 0), CUDA_SUCCESS, "cuGetProcAddress supported");
    if (resolved == NULL) fail("cuGetProcAddress returned NULL for supported symbol");
    resolved = (void *)1;
    require_result(cuGetProcAddress("cuDefinitelyUnsupported", &resolved, 12080, 0), CUDA_ERROR_NOT_FOUND, "cuGetProcAddress unsupported");
    if (resolved != NULL) fail("cuGetProcAddress retained pointer for unsupported symbol");

    require_result(cuLaunchKernel(NULL, 1, 1, 1, 1, 1, 1, 0, NULL, NULL, NULL), CUDA_ERROR_NOT_SUPPORTED, "cuLaunchKernel");
    dim3 one = {1, 1, 1};
    require_result(cudaLaunchKernel(NULL, one, one, NULL, 0, NULL), cudaErrorNotSupported, "cudaLaunchKernel");
    const char *error_text = cudaGetErrorString(cudaErrorNotSupported);
    if (error_text == NULL || strstr(error_text, "not supported") == NULL) fail("unsupported error string is unclear");

    puts("basic-ok");
    return 0;
}
