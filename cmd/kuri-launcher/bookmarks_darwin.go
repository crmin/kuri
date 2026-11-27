//go:build cgo

package main

/*
#include <stdlib.h>
#include "bookmarks_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"time"
	"unsafe"
)

//export kuriGoBookmarkRequest
func kuriGoBookmarkRequest(request *C.char, sequence C.uint64_t) {
	input := []byte(C.GoString(request))
	go func() {
		result := settingsRunner.runInput(context.Background(), 10*time.Second, time.Second, input, "__bookmarks")
		response := result.stdout
		if result.err != nil || !json.Valid(response) {
			message := result.diagnostic
			if message == "" || result.timedOut {
				message = "Could not complete the bookmark request. Try again."
			}
			response, _ = json.Marshal(map[string]string{"error": message})
		}
		payload := C.CString(string(response))
		defer C.free(unsafe.Pointer(payload))
		C.kuriRenderBookmarks(payload, sequence)
	}()
}
