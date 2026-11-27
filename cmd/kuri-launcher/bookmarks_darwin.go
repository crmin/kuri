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
	"sync"
	"time"
	"unsafe"
)

var bookmarkRequests sync.Map

//export kuriGoBookmarkRequest
func kuriGoBookmarkRequest(request *C.char, sequence C.uint64_t) {
	input := []byte(C.GoString(request))
	ctx, cancel := context.WithCancel(context.Background())
	bookmarkRequests.Store(sequence, cancel)
	go func() {
		defer cancel()
		defer bookmarkRequests.Delete(sequence)
		result := settingsRunner.runInput(ctx, 10*time.Second, time.Second, input, "__bookmarks")
		if ctx.Err() != nil {
			return
		}
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

//export kuriGoCancelBookmarkRequest
func kuriGoCancelBookmarkRequest(sequence C.uint64_t) {
	if cancel, ok := bookmarkRequests.LoadAndDelete(sequence); ok {
		cancel.(context.CancelFunc)()
	}
}
