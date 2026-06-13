package upload

/*
#include <stdlib.h>
#include "upload.h"
*/
import "C"
import (
	"math"
	"unsafe"

	"github.com/dunglas/frankenphp"
)

func init() {
	frankenphp.RegisterExtension(unsafe.Pointer(&C.pogo_upload_module_entry))
}

//export pogo_upload_create
func pogo_upload_create(intentJSON *C.char, intentJSONLen C.size_t, storeName *C.char, storeNameLen C.size_t, errKind *C.int, errOut **C.char) *C.char {
	intent, ok := goStringFromC(intentJSON, intentJSONLen)
	if !ok {
		setCError(valueError("upload intent is too large"), errKind, errOut)
		return nil
	}
	store, ok := goStringFromC(storeName, storeNameLen)
	if !ok {
		setCError(valueError("store name is too large"), errKind, errOut)
		return nil
	}

	response, apiErr := currentManager().create(store, intent)
	if apiErr != nil {
		setCError(apiErr, errKind, errOut)
		return nil
	}

	return C.CString(response)
}

//export pogo_upload_progress
func pogo_upload_progress(uploadID *C.char, uploadIDLen C.size_t, storeName *C.char, storeNameLen C.size_t, errKind *C.int, errOut **C.char) *C.char {
	id, ok := goStringFromC(uploadID, uploadIDLen)
	if !ok {
		setCError(valueError("upload id is too large"), errKind, errOut)
		return nil
	}
	store, ok := goStringFromC(storeName, storeNameLen)
	if !ok {
		setCError(valueError("store name is too large"), errKind, errOut)
		return nil
	}

	response, apiErr := currentManager().progress(store, id)
	if apiErr != nil {
		setCError(apiErr, errKind, errOut)
		return nil
	}
	if response == "" {
		return nil
	}

	return C.CString(response)
}

//export pogo_upload_cancel
func pogo_upload_cancel(uploadID *C.char, uploadIDLen C.size_t, storeName *C.char, storeNameLen C.size_t, errKind *C.int, errOut **C.char) C.int {
	id, ok := goStringFromC(uploadID, uploadIDLen)
	if !ok {
		setCError(valueError("upload id is too large"), errKind, errOut)
		return 0
	}
	store, ok := goStringFromC(storeName, storeNameLen)
	if !ok {
		setCError(valueError("store name is too large"), errKind, errOut)
		return 0
	}

	cancelled, apiErr := currentManager().cancelUpload(store, id)
	if apiErr != nil {
		setCError(apiErr, errKind, errOut)
		return 0
	}
	if cancelled {
		return 1
	}
	return 0
}

//export pogo_upload_status
func pogo_upload_status(storeName *C.char, storeNameLen C.size_t, errKind *C.int, errOut **C.char) *C.char {
	store, ok := goStringFromC(storeName, storeNameLen)
	if !ok {
		setCError(valueError("store name is too large"), errKind, errOut)
		return nil
	}

	response, apiErr := currentManager().status(store)
	if apiErr != nil {
		setCError(apiErr, errKind, errOut)
		return nil
	}

	return C.CString(response)
}

func goStringFromC(ptr *C.char, length C.size_t) (string, bool) {
	if ptr == nil || length == 0 {
		return "", true
	}
	if uint64(length) > uint64(math.MaxInt32) {
		return "", false
	}
	return C.GoStringN(ptr, C.int(length)), true
}

func setCError(apiErr *apiError, errKind *C.int, errOut **C.char) {
	if apiErr == nil {
		return
	}
	if errKind != nil {
		*errKind = C.int(apiErr.kind)
	}
	if errOut != nil {
		*errOut = C.CString(apiErr.Error())
	}
}
