//go:build linux && cgo

package main

/*
#cgo LDFLAGS: -ldl
#include <stdlib.h>
#include "cuda_abi.h"
*/
import "C"

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	fakecuda "github.com/brantje/fake-nvidia/internal/cuda"
)

var (
	engineOnce sync.Once
	engineInst *fakecuda.Engine
	engineErr  error
	lastError  atomic.Int32
)

func getEngine() (*fakecuda.Engine, error) {
	engineOnce.Do(func() {
		backend, err := fakecuda.NewControlBackendFromEnv()
		if err != nil {
			engineErr = err
			return
		}
		policy, err := fakecuda.FaultPolicyFromEnv()
		if err != nil {
			engineErr = err
			return
		}
		engineInst, engineErr = fakecuda.NewEngine(backend, policy)
	})
	return engineInst, engineErr
}

func apiContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 15*time.Second)
}

func driverStatus(status fakecuda.Status) C.CUresult {
	switch status {
	case fakecuda.StatusSuccess:
		return C.FAKE_CUDA_DRIVER_SUCCESS
	case fakecuda.StatusInvalidValue:
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	case fakecuda.StatusOutOfMemory:
		return C.FAKE_CUDA_DRIVER_ERROR_OUT_OF_MEMORY
	case fakecuda.StatusInitialization:
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	case fakecuda.StatusNoDevice:
		return C.FAKE_CUDA_DRIVER_ERROR_NO_DEVICE
	case fakecuda.StatusInvalidDevice:
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_DEVICE
	case fakecuda.StatusNotFound:
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_FOUND
	case fakecuda.StatusNotSupported:
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
	default:
		return C.FAKE_CUDA_DRIVER_ERROR_UNKNOWN
	}
}

func runtimeStatus(status fakecuda.Status) C.cudaError_t {
	var result C.cudaError_t
	switch status {
	case fakecuda.StatusSuccess:
		result = C.FAKE_CUDA_SUCCESS
	case fakecuda.StatusInvalidValue:
		result = C.FAKE_CUDA_ERROR_INVALID_VALUE
	case fakecuda.StatusOutOfMemory:
		result = C.FAKE_CUDA_ERROR_MEMORY_ALLOCATION
	case fakecuda.StatusInitialization:
		result = C.FAKE_CUDA_ERROR_INITIALIZATION
	case fakecuda.StatusNoDevice:
		result = C.FAKE_CUDA_ERROR_NO_DEVICE
	case fakecuda.StatusInvalidDevice:
		result = C.FAKE_CUDA_ERROR_INVALID_DEVICE
	case fakecuda.StatusNotSupported, fakecuda.StatusNotFound:
		result = C.FAKE_CUDA_ERROR_NOT_SUPPORTED
	default:
		result = C.FAKE_CUDA_ERROR_UNKNOWN
	}
	if result != C.FAKE_CUDA_SUCCESS {
		lastError.Store(int32(result))
	}
	return result
}

func device(index int) (fakecuda.Device, fakecuda.Status) {
	engine, err := getEngine()
	if err != nil {
		return fakecuda.Device{}, fakecuda.StatusInitialization
	}
	ctx, cancel := apiContext()
	defer cancel()
	return engine.Device(ctx, index)
}

func cString(value string) (*C.char, func()) {
	ptr := C.CString(value)
	return ptr, func() { C.free(unsafe.Pointer(ptr)) }
}

//export cuInit
func cuInit(flags C.uint) C.CUresult {
	if flags != 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	ctx, cancel := apiContext()
	defer cancel()
	return driverStatus(engine.Init(ctx))
}

//export cuDriverGetVersion
func cuDriverGetVersion(version *C.int) C.CUresult {
	if version == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	value, status := engine.CUDAVersion()
	if status == fakecuda.StatusSuccess {
		*version = C.int(value)
	}
	return driverStatus(status)
}

//export cuDeviceGetCount
func cuDeviceGetCount(count *C.int) C.CUresult {
	if count == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	ctx, cancel := apiContext()
	defer cancel()
	value, status := engine.DeviceCount(ctx)
	if status == fakecuda.StatusSuccess {
		*count = C.int(value)
	}
	return driverStatus(status)
}

//export cuDeviceGet
func cuDeviceGet(out *C.CUdevice, ordinal C.int) C.CUresult {
	if out == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	_, status := device(int(ordinal))
	if status == fakecuda.StatusSuccess {
		*out = C.CUdevice(ordinal)
	}
	return driverStatus(status)
}

//export cuDeviceGetName
func cuDeviceGetName(name *C.char, length C.int, dev C.CUdevice) C.CUresult {
	if name == nil || length <= 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status != fakecuda.StatusSuccess {
		return driverStatus(status)
	}
	value, done := cString(info.Name)
	defer done()
	C.fake_cuda_copy_string(name, length, value)
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuDeviceGetUuid
func cuDeviceGetUuid(uuid *C.CUuuid, dev C.CUdevice) C.CUresult {
	if uuid == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status != fakecuda.StatusSuccess {
		return driverStatus(status)
	}
	value, done := cString(info.UUID)
	defer done()
	if C.fake_cuda_parse_uuid(value, (*C.uchar)(unsafe.Pointer(uuid))) == 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_UNKNOWN
	}
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuDeviceGetUuid_v2
func cuDeviceGetUuid_v2(uuid *C.CUuuid, dev C.CUdevice) C.CUresult {
	return cuDeviceGetUuid(uuid, dev)
}

//export cuDeviceGetPCIBusId
func cuDeviceGetPCIBusId(busID *C.char, length C.int, dev C.CUdevice) C.CUresult {
	if busID == nil || length <= 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status != fakecuda.StatusSuccess {
		return driverStatus(status)
	}
	value, done := cString(info.PCIBusID)
	defer done()
	C.fake_cuda_copy_string(busID, length, value)
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuDeviceTotalMem
func cuDeviceTotalMem(bytes *C.size_t, dev C.CUdevice) C.CUresult {
	if bytes == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status == fakecuda.StatusSuccess {
		*bytes = C.size_t(info.TotalBytes)
	}
	return driverStatus(status)
}

//export cuDeviceTotalMem_v2
func cuDeviceTotalMem_v2(bytes *C.size_t, dev C.CUdevice) C.CUresult {
	return cuDeviceTotalMem(bytes, dev)
}

//export cuDeviceComputeCapability
func cuDeviceComputeCapability(major, minor *C.int, dev C.CUdevice) C.CUresult {
	if major == nil || minor == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status != fakecuda.StatusSuccess {
		return driverStatus(status)
	}
	if info.ComputeMajor <= 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
	}
	*major = C.int(info.ComputeMajor)
	*minor = C.int(info.ComputeMinor)
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuDeviceGetAttribute
func cuDeviceGetAttribute(value *C.int, attribute C.CUdevice_attribute, dev C.CUdevice) C.CUresult {
	if value == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	info, status := device(int(dev))
	if status != fakecuda.StatusSuccess {
		return driverStatus(status)
	}
	var out int
	switch int(attribute) {
	case 1:
		out = 1024
	case 2, 3:
		out = 1024
	case 4:
		out = 64
	case 5:
		out = 2147483647
	case 6, 7:
		out = 65535
	case 8:
		out = 48 * 1024
	case 9:
		out = 64 * 1024
	case 10:
		out = 32
	case 11:
		out = 2147483647
	case 12:
		out = 65536
	case 41:
		out = 1
	case 75:
		out = info.ComputeMajor
	case 76:
		out = info.ComputeMinor
	case 83:
		out = 0
	default:
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
	}
	*value = C.int(out)
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuMemGetInfo
func cuMemGetInfo(freeBytes, totalBytes *C.size_t) C.CUresult {
	if freeBytes == nil || totalBytes == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	ctx, cancel := apiContext()
	defer cancel()
	free, total, status := engine.MemoryInfo(ctx)
	if status == fakecuda.StatusSuccess {
		*freeBytes = C.size_t(free)
		*totalBytes = C.size_t(total)
	}
	return driverStatus(status)
}

//export cuMemGetInfo_v2
func cuMemGetInfo_v2(freeBytes, totalBytes *C.size_t) C.CUresult {
	return cuMemGetInfo(freeBytes, totalBytes)
}

//export cuMemAlloc
func cuMemAlloc(out *C.CUdeviceptr, bytes C.size_t) C.CUresult {
	if out == nil || bytes == 0 {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	token := C.fake_cuda_alloc_token()
	if token == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_OUT_OF_MEMORY
	}
	ctx, cancel := apiContext()
	defer cancel()
	status := engine.TrackAllocation(ctx, uintptr(C.fake_cuda_token_value(token)), uint64(bytes))
	if status != fakecuda.StatusSuccess {
		C.fake_cuda_free_token(token)
		return driverStatus(status)
	}
	*out = C.CUdeviceptr(C.fake_cuda_token_value(token))
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuMemAlloc_v2
func cuMemAlloc_v2(out *C.CUdeviceptr, bytes C.size_t) C.CUresult {
	return cuMemAlloc(out, bytes)
}

//export cuMemFree
func cuMemFree(ptr C.CUdeviceptr) C.CUresult {
	if ptr == 0 {
		return C.FAKE_CUDA_DRIVER_SUCCESS
	}
	engine, err := getEngine()
	if err != nil {
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_INITIALIZED
	}
	ctx, cancel := apiContext()
	defer cancel()
	status := engine.FreeAllocation(ctx, uintptr(ptr))
	if status == fakecuda.StatusSuccess {
		C.fake_cuda_free_token(C.fake_cuda_token_pointer(C.uintptr_t(ptr)))
	}
	return driverStatus(status)
}

//export cuMemFree_v2
func cuMemFree_v2(ptr C.CUdeviceptr) C.CUresult {
	return cuMemFree(ptr)
}

//export cuGetErrorName
func cuGetErrorName(code C.CUresult, name **C.char) C.CUresult {
	if name == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	*name = (*C.char)(unsafe.Pointer(C.fake_cuda_driver_error_name(C.int(code))))
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuGetErrorString
func cuGetErrorString(code C.CUresult, text **C.char) C.CUresult {
	if text == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	*text = (*C.char)(unsafe.Pointer(C.fake_cuda_driver_error_string(C.int(code))))
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuGetProcAddress
func cuGetProcAddress(symbol *C.char, pfn *unsafe.Pointer, cudaVersion C.int, flags C.cuuint64_t) C.CUresult {
	if symbol == nil || pfn == nil {
		return C.FAKE_CUDA_DRIVER_ERROR_INVALID_VALUE
	}
	found := C.fake_cuda_lookup_symbol(symbol)
	if found == nil {
		*pfn = nil
		return C.FAKE_CUDA_DRIVER_ERROR_NOT_FOUND
	}
	*pfn = found
	return C.FAKE_CUDA_DRIVER_SUCCESS
}

//export cuGetProcAddress_v2
func cuGetProcAddress_v2(symbol *C.char, pfn *unsafe.Pointer, cudaVersion C.int, flags C.cuuint64_t, query *C.CUdriverProcAddressQueryResult) C.CUresult {
	result := cuGetProcAddress(symbol, pfn, cudaVersion, flags)
	if query != nil {
		if result == C.FAKE_CUDA_DRIVER_SUCCESS {
			*query = 0
		} else {
			*query = 1
		}
	}
	return result
}

//export cuModuleLoadData
func cuModuleLoadData(module *C.CUmodule, image unsafe.Pointer) C.CUresult {
	return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
}

//export cuModuleLoadDataEx
func cuModuleLoadDataEx(module *C.CUmodule, image unsafe.Pointer, numOptions C.uint, options *C.CUjit_option, optionValues *unsafe.Pointer) C.CUresult {
	return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
}

//export cuModuleGetFunction
func cuModuleGetFunction(function *C.CUfunction, module C.CUmodule, name *C.char) C.CUresult {
	return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
}

//export cuLaunchKernel
func cuLaunchKernel(function C.CUfunction, gridX, gridY, gridZ, blockX, blockY, blockZ C.uint, sharedMem C.uint, stream C.CUstream, kernelParams, extra *unsafe.Pointer) C.CUresult {
	return C.FAKE_CUDA_DRIVER_ERROR_NOT_SUPPORTED
}

//export cudaDriverGetVersion
func cudaDriverGetVersion(version *C.int) C.cudaError_t {
	if version == nil {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	value, status := engine.CUDAVersion()
	if status == fakecuda.StatusSuccess {
		*version = C.int(value)
	}
	return runtimeStatus(status)
}

//export cudaRuntimeGetVersion
func cudaRuntimeGetVersion(version *C.int) C.cudaError_t {
	return cudaDriverGetVersion(version)
}

//export cudaGetDeviceCount
func cudaGetDeviceCount(count *C.int) C.cudaError_t {
	if count == nil {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	ctx, cancel := apiContext()
	defer cancel()
	value, status := engine.DeviceCount(ctx)
	if status == fakecuda.StatusSuccess {
		*count = C.int(value)
	}
	return runtimeStatus(status)
}

//export cudaSetDevice
func cudaSetDevice(index C.int) C.cudaError_t {
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	ctx, cancel := apiContext()
	defer cancel()
	return runtimeStatus(engine.SetDevice(ctx, int(index)))
}

//export cudaGetDevice
func cudaGetDevice(index *C.int) C.cudaError_t {
	if index == nil {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	value, status := engine.CurrentDevice()
	if status == fakecuda.StatusSuccess {
		*index = C.int(value)
	}
	return runtimeStatus(status)
}

//export cudaDeviceReset
func cudaDeviceReset() C.cudaError_t {
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	ctx, cancel := apiContext()
	defer cancel()
	return runtimeStatus(engine.ResetDevice(ctx))
}

//export cudaMemGetInfo
func cudaMemGetInfo(freeBytes, totalBytes *C.size_t) C.cudaError_t {
	if freeBytes == nil || totalBytes == nil {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	ctx, cancel := apiContext()
	defer cancel()
	free, total, status := engine.MemoryInfo(ctx)
	if status == fakecuda.StatusSuccess {
		*freeBytes = C.size_t(free)
		*totalBytes = C.size_t(total)
	}
	return runtimeStatus(status)
}

//export cudaMalloc
func cudaMalloc(out *unsafe.Pointer, bytes C.size_t) C.cudaError_t {
	if out == nil || bytes == 0 {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	token := C.fake_cuda_alloc_token()
	if token == nil {
		return runtimeStatus(fakecuda.StatusOutOfMemory)
	}
	ctx, cancel := apiContext()
	defer cancel()
	status := engine.TrackAllocation(ctx, uintptr(C.fake_cuda_token_value(token)), uint64(bytes))
	if status != fakecuda.StatusSuccess {
		C.fake_cuda_free_token(token)
		return runtimeStatus(status)
	}
	*out = token
	return runtimeStatus(fakecuda.StatusSuccess)
}

//export cudaFree
func cudaFree(ptr unsafe.Pointer) C.cudaError_t {
	if ptr == nil {
		return runtimeStatus(fakecuda.StatusSuccess)
	}
	engine, err := getEngine()
	if err != nil {
		return runtimeStatus(fakecuda.StatusInitialization)
	}
	ctx, cancel := apiContext()
	defer cancel()
	status := engine.FreeAllocation(ctx, uintptr(C.fake_cuda_token_value(ptr)))
	if status == fakecuda.StatusSuccess {
		C.fake_cuda_free_token(ptr)
	}
	return runtimeStatus(status)
}

//export cudaGetDeviceProperties
func cudaGetDeviceProperties(properties unsafe.Pointer, index C.int) C.cudaError_t {
	if properties == nil {
		return runtimeStatus(fakecuda.StatusInvalidValue)
	}
	info, status := device(int(index))
	if status != fakecuda.StatusSuccess {
		return runtimeStatus(status)
	}
	if info.ComputeMajor <= 0 {
		return runtimeStatus(fakecuda.StatusNotSupported)
	}
	name, freeName := cString(info.Name)
	defer freeName()
	uuid, freeUUID := cString(info.UUID)
	defer freeUUID()
	C.fake_cuda_fill_properties(properties, name, uuid, C.size_t(info.TotalBytes), C.int(info.ComputeMajor), C.int(info.ComputeMinor))
	return runtimeStatus(fakecuda.StatusSuccess)
}

//export cudaGetDeviceProperties_v2
func cudaGetDeviceProperties_v2(properties unsafe.Pointer, index C.int) C.cudaError_t {
	return cudaGetDeviceProperties(properties, index)
}

//export cudaMemcpy
func cudaMemcpy(dst, src unsafe.Pointer, count C.size_t, kind C.int) C.cudaError_t {
	if count == 0 {
		return runtimeStatus(fakecuda.StatusSuccess)
	}
	if kind == 0 {
		if dst == nil || src == nil {
			return runtimeStatus(fakecuda.StatusInvalidValue)
		}
		C.fake_cuda_memcpy_host(dst, src, count)
		return runtimeStatus(fakecuda.StatusSuccess)
	}
	return runtimeStatus(fakecuda.StatusNotSupported)
}

//export cudaLaunchKernel
func cudaLaunchKernel(function unsafe.Pointer, gridDim, blockDim C.dim3, args *unsafe.Pointer, sharedMem C.size_t, stream C.cudaStream_t) C.cudaError_t {
	return runtimeStatus(fakecuda.StatusNotSupported)
}

//export cudaDeviceSynchronize
func cudaDeviceSynchronize() C.cudaError_t {
	return runtimeStatus(fakecuda.StatusSuccess)
}

//export cudaGetErrorName
func cudaGetErrorName(code C.cudaError_t) *C.char {
	return (*C.char)(unsafe.Pointer(C.fake_cuda_runtime_error_name(C.int(code))))
}

//export cudaGetErrorString
func cudaGetErrorString(code C.cudaError_t) *C.char {
	return (*C.char)(unsafe.Pointer(C.fake_cuda_runtime_error_string(C.int(code))))
}

//export cudaPeekAtLastError
func cudaPeekAtLastError() C.cudaError_t {
	return C.cudaError_t(lastError.Load())
}

//export cudaGetLastError
func cudaGetLastError() C.cudaError_t {
	return C.cudaError_t(lastError.Swap(0))
}
