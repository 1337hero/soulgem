package tts

// This file holds the Go functions qwentts.cpp calls back into. cgo forbids
// definitions in the preamble of a file using //export, so the C trampolines
// that reference them live in native.go.

/*
#include <stdbool.h>
#include <stdint.h>
#include <stdlib.h>
*/
import "C"

import (
	"runtime/cgo"
	"unsafe"
)

// handle keeps a cgo.Handle in C memory so the void* the ABI carries is a real
// pointer to a word rather than a Go integer laundered through unsafe.Pointer.
type handle struct {
	value cgo.Handle
	slot  *C.uintptr_t
}

func newHandle(state *callbackState) handle {
	value := cgo.NewHandle(state)
	slot := (*C.uintptr_t)(C.malloc(C.size_t(unsafe.Sizeof(C.uintptr_t(0)))))
	*slot = C.uintptr_t(value)
	return handle{value: value, slot: slot}
}

func (h handle) pointer() unsafe.Pointer { return unsafe.Pointer(h.slot) }

func (h handle) Delete() {
	C.free(unsafe.Pointer(h.slot))
	h.value.Delete()
}

func stateFor(user unsafe.Pointer) *callbackState {
	return cgo.Handle(*(*C.uintptr_t)(user)).Value().(*callbackState)
}

//export qtGoCancel
func qtGoCancel(user unsafe.Pointer) C.bool {
	return C.bool(stateFor(user).stop())
}

//export qtGoChunk
func qtGoChunk(samples *C.float, n C.int, user unsafe.Pointer) C.bool {
	state := stateFor(user)
	if state.stop() {
		return C.bool(false)
	}
	if n > 0 && samples != nil {
		if !state.onChunk(unsafe.Slice((*float32)(unsafe.Pointer(samples)), int(n))) {
			state.canceled = true
			return C.bool(false)
		}
	}
	return C.bool(true)
}
