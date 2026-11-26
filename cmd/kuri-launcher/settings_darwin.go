//go:build cgo

package main

/*
#include <stdlib.h>
#include "settings_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"time"
	"unsafe"
)

var settingsRunner commandRunner

//export kuriGoSettingsRequest
func kuriGoSettingsRequest(edit *C.char) {
	var input []byte
	command := "__settings_get"
	if edit != nil {
		command = "__settings_apply"
		input = []byte(C.GoString(edit))
	}
	go func() {
		timeout := 10 * time.Second
		if input != nil {
			timeout = 8 * time.Minute
		}
		result := settingsRunner.runInput(context.Background(), timeout, 30*time.Second, input, command)
		response := result.stdout
		if result.err != nil || !json.Valid(response) {
			message := result.diagnostic
			if message == "" {
				message = "Could not read kuri settings."
			}
			response, _ = json.Marshal(map[string]string{"error": message})
		}
		payload := C.CString(string(response))
		defer C.free(unsafe.Pointer(payload))
		C.kuriRenderSettings(payload)
		if input != nil && menu != nil {
			menu.refresh()
		}
	}()
}
