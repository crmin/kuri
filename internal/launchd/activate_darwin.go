//go:build darwin && cgo

package launchd

/*
#include <launch.h>
#include <stdlib.h>
*/
import "C"

import (
	"syscall"
	"unsafe"
)

func activateDescriptors(name string) ([]int, error) {
	key := C.CString(name)
	defer C.free(unsafe.Pointer(key))
	var fds *C.int
	var count C.size_t
	if code := C.launch_activate_socket(key, &fds, &count); code != 0 {
		return nil, syscall.Errno(code)
	}
	defer C.free(unsafe.Pointer(fds))
	result := make([]int, int(count))
	for i, fd := range unsafe.Slice(fds, int(count)) {
		result[i] = int(fd)
	}
	return result, nil
}
