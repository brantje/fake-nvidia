//go:build linux && cgo

package nvmlpmon

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#define NVML_SUCCESS 0
#define NVML_ERROR_FUNCTION_NOT_FOUND 13

struct nvmlDevice_st;
typedef struct {
	struct nvmlDevice_st* handle;
} nvmlDevice_t;

typedef struct nvmlProcessUtilizationSample_st {
	unsigned int pid;
	unsigned long long timeStamp;
	unsigned int smUtil;
	unsigned int memUtil;
	unsigned int encUtil;
	unsigned int decUtil;
} nvmlProcessUtilizationSample_t;

typedef int (*nvmlInit_fn)(void);
typedef int (*nvmlShutdown_fn)(void);
typedef int (*nvmlDeviceGetCount_fn)(unsigned int*);
typedef int (*nvmlDeviceGetHandleByIndex_fn)(unsigned int, nvmlDevice_t*);
typedef int (*nvmlDeviceGetProcessUtilization_fn)(nvmlDevice_t, nvmlProcessUtilizationSample_t*, unsigned int*, unsigned long long);
typedef int (*nvmlSystemGetProcessName_fn)(unsigned int, char*, unsigned int);

static void* nvml_lib = NULL;
static nvmlInit_fn p_nvmlInit = NULL;
static nvmlShutdown_fn p_nvmlShutdown = NULL;
static nvmlDeviceGetCount_fn p_nvmlDeviceGetCount = NULL;
static nvmlDeviceGetHandleByIndex_fn p_nvmlDeviceGetHandleByIndex = NULL;
static nvmlDeviceGetProcessUtilization_fn p_nvmlDeviceGetProcessUtilization = NULL;
static nvmlSystemGetProcessName_fn p_nvmlSystemGetProcessName = NULL;
static char nvml_error[512];

static void set_nvml_error(const char* text) {
	if (text == NULL) {
		text = "unknown dynamic loader error";
	}
	snprintf(nvml_error, sizeof(nvml_error), "%s", text);
}

static void* required_symbol(const char* name) {
	dlerror();
	void* symbol = dlsym(nvml_lib, name);
	const char* err = dlerror();
	if (err != NULL) {
		set_nvml_error(err);
		return NULL;
	}
	return symbol;
}

static void* optional_symbol(const char* primary, const char* fallback) {
	dlerror();
	void* symbol = dlsym(nvml_lib, primary);
	if (dlerror() == NULL && symbol != NULL) {
		return symbol;
	}
	if (fallback == NULL) {
		return NULL;
	}
	dlerror();
	symbol = dlsym(nvml_lib, fallback);
	if (dlerror() != NULL) {
		return NULL;
	}
	return symbol;
}

static int fnvml_open(void) {
	if (nvml_lib != NULL) {
		return 0;
	}
	nvml_error[0] = '\0';
	nvml_lib = dlopen("libnvidia-ml.so.1", RTLD_NOW | RTLD_LOCAL);
	if (nvml_lib == NULL) {
		nvml_lib = dlopen("libnvidia-ml.so", RTLD_NOW | RTLD_LOCAL);
	}
	if (nvml_lib == NULL) {
		set_nvml_error(dlerror());
		return -1;
	}

	p_nvmlInit = (nvmlInit_fn)optional_symbol("nvmlInit_v2", "nvmlInit");
	p_nvmlShutdown = (nvmlShutdown_fn)required_symbol("nvmlShutdown");
	p_nvmlDeviceGetCount = (nvmlDeviceGetCount_fn)optional_symbol("nvmlDeviceGetCount_v2", "nvmlDeviceGetCount");
	p_nvmlDeviceGetHandleByIndex = (nvmlDeviceGetHandleByIndex_fn)optional_symbol("nvmlDeviceGetHandleByIndex_v2", "nvmlDeviceGetHandleByIndex");
	p_nvmlDeviceGetProcessUtilization = (nvmlDeviceGetProcessUtilization_fn)required_symbol("nvmlDeviceGetProcessUtilization");
	p_nvmlSystemGetProcessName = (nvmlSystemGetProcessName_fn)optional_symbol("nvmlSystemGetProcessName", NULL);

	if (p_nvmlInit == NULL || p_nvmlShutdown == NULL || p_nvmlDeviceGetCount == NULL ||
		p_nvmlDeviceGetHandleByIndex == NULL || p_nvmlDeviceGetProcessUtilization == NULL) {
		if (nvml_error[0] == '\0') {
			set_nvml_error("required NVML symbol is unavailable");
		}
		dlclose(nvml_lib);
		nvml_lib = NULL;
		return -1;
	}
	return 0;
}

static const char* fnvml_error(void) {
	return nvml_error;
}

static int fnvml_init(void) {
	return p_nvmlInit();
}

static int fnvml_shutdown(void) {
	if (p_nvmlShutdown == NULL) {
		return NVML_SUCCESS;
	}
	return p_nvmlShutdown();
}

static int fnvml_device_count(unsigned int* count) {
	return p_nvmlDeviceGetCount(count);
}

static int fnvml_device_handle(unsigned int index, nvmlDevice_t* device) {
	return p_nvmlDeviceGetHandleByIndex(index, device);
}

static int fnvml_process_utilization(nvmlDevice_t device, nvmlProcessUtilizationSample_t* samples, unsigned int* count) {
	return p_nvmlDeviceGetProcessUtilization(device, samples, count, 0);
}

static int fnvml_process_name(unsigned int pid, char* name, unsigned int length) {
	if (p_nvmlSystemGetProcessName == NULL) {
		return NVML_ERROR_FUNCTION_NOT_FOUND;
	}
	return p_nvmlSystemGetProcessName(pid, name, length);
}

static void fnvml_close(void) {
	if (nvml_lib != NULL) {
		dlclose(nvml_lib);
	}
	nvml_lib = NULL;
	p_nvmlInit = NULL;
	p_nvmlShutdown = NULL;
	p_nvmlDeviceGetCount = NULL;
	p_nvmlDeviceGetHandleByIndex = NULL;
	p_nvmlDeviceGetProcessUtilization = NULL;
	p_nvmlSystemGetProcessName = NULL;
}
*/
import "C"

import (
	"fmt"
	"unsafe"

	"github.com/brantje/fake-nvidia/internal/pmon"
)

const processNameBufferSize = 4096

// Collect returns current per-process utilization from the NVML library loaded
// by the fake-nvidia runtime environment.
func Collect() ([]pmon.Sample, error) {
	if C.fnvml_open() != 0 {
		return nil, fmt.Errorf("load NVML: %s", C.GoString(C.fnvml_error()))
	}
	defer C.fnvml_close()

	if ret := int(C.fnvml_init()); ret != 0 {
		return nil, fmt.Errorf("nvmlInit: return code %d", ret)
	}
	defer C.fnvml_shutdown()

	var deviceCount C.uint
	if ret := int(C.fnvml_device_count(&deviceCount)); ret != 0 {
		return nil, fmt.Errorf("nvmlDeviceGetCount: return code %d", ret)
	}

	var result []pmon.Sample
	for index := C.uint(0); index < deviceCount; index++ {
		var device C.nvmlDevice_t
		if ret := int(C.fnvml_device_handle(index, &device)); ret != 0 {
			return nil, fmt.Errorf("nvmlDeviceGetHandleByIndex(%d): return code %d", uint32(index), ret)
		}

		var sampleCount C.uint
		probeRet := int(C.fnvml_process_utilization(device, nil, &sampleCount))
		if sampleCount == 0 {
			if probeRet != 0 {
				return nil, fmt.Errorf("nvmlDeviceGetProcessUtilization(%d) probe: return code %d", uint32(index), probeRet)
			}
			continue
		}
		if probeRet != 0 && probeRet != 7 {
			return nil, fmt.Errorf("nvmlDeviceGetProcessUtilization(%d) probe: return code %d", uint32(index), probeRet)
		}

		buffer := C.malloc(C.size_t(sampleCount) * C.size_t(C.sizeof_nvmlProcessUtilizationSample_t))
		if buffer == nil {
			return nil, fmt.Errorf("allocate %d NVML process samples", uint32(sampleCount))
		}
		fillCount := sampleCount
		ret := int(C.fnvml_process_utilization(device, (*C.nvmlProcessUtilizationSample_t)(buffer), &fillCount))
		if ret != 0 {
			C.free(buffer)
			return nil, fmt.Errorf("nvmlDeviceGetProcessUtilization(%d): return code %d", uint32(index), ret)
		}

		samples := unsafe.Slice((*C.nvmlProcessUtilizationSample_t)(buffer), int(fillCount))
		for _, sample := range samples {
			result = append(result, pmon.Sample{
				GPUIndex:    int(index),
				PID:         uint32(sample.pid),
				Type:        "C",
				SMUtil:      uint32(sample.smUtil),
				MemoryUtil:  uint32(sample.memUtil),
				EncoderUtil: uint32(sample.encUtil),
				DecoderUtil: uint32(sample.decUtil),
				Name:        processName(uint32(sample.pid)),
			})
		}
		C.free(buffer)
	}
	return result, nil
}

func processName(pid uint32) string {
	buffer := C.malloc(processNameBufferSize)
	if buffer == nil {
		return ""
	}
	defer C.free(buffer)
	if ret := int(C.fnvml_process_name(C.uint(pid), (*C.char)(buffer), C.uint(processNameBufferSize))); ret != 0 {
		return ""
	}
	return C.GoString((*C.char)(buffer))
}
