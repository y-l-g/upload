/* This is a generated-compatible file, edit upload.stub.php and regenerate when tooling is available. */

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_pogo_upload_create, 0, 1, IS_ARRAY, 0)
	ZEND_ARG_TYPE_INFO(0, intent, IS_ARRAY, 0)
	ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, store, IS_STRING, 0, "\"default\"")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_pogo_upload_progress, 0, 1, IS_ARRAY, 1)
	ZEND_ARG_TYPE_INFO(0, uploadId, IS_STRING, 0)
	ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, store, IS_STRING, 0, "\"default\"")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_pogo_upload_cancel, 0, 1, _IS_BOOL, 0)
	ZEND_ARG_TYPE_INFO(0, uploadId, IS_STRING, 0)
	ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, store, IS_STRING, 0, "\"default\"")
ZEND_END_ARG_INFO()

ZEND_BEGIN_ARG_WITH_RETURN_TYPE_INFO_EX(arginfo_pogo_upload_status, 0, 0, IS_STRING, 0)
	ZEND_ARG_TYPE_INFO_WITH_DEFAULT_VALUE(0, store, IS_STRING, 1, "null")
ZEND_END_ARG_INFO()

ZEND_FUNCTION(pogo_upload_create);
ZEND_FUNCTION(pogo_upload_progress);
ZEND_FUNCTION(pogo_upload_cancel);
ZEND_FUNCTION(pogo_upload_status);

static const zend_function_entry ext_functions[] = {
	ZEND_FE(pogo_upload_create, arginfo_pogo_upload_create)
	ZEND_FE(pogo_upload_progress, arginfo_pogo_upload_progress)
	ZEND_FE(pogo_upload_cancel, arginfo_pogo_upload_cancel)
	ZEND_FE(pogo_upload_status, arginfo_pogo_upload_status)
	ZEND_FE_END
};
