#include <php.h>
#include <Zend/zend_exceptions.h>
#include <Zend/zend_smart_str.h>
#include <ext/json/php_json.h>
#include <ext/spl/spl_exceptions.h>
#include <stdlib.h>
#include <string.h>

#include "_cgo_export.h"
#include "upload.h"
#include "upload_arginfo.h"

#define POGO_UPLOAD_ERR_VALUE 1

PHP_MINIT_FUNCTION(pogo_upload)
{
	return SUCCESS;
}

zend_module_entry pogo_upload_module_entry = {
	STANDARD_MODULE_HEADER,
	"pogo_upload",
	ext_functions,
	PHP_MINIT(pogo_upload),
	NULL,
	NULL,
	NULL,
	NULL,
	"0.1.0",
	STANDARD_MODULE_PROPERTIES
};

static zend_class_entry *runtime_exception_ce(void)
{
	return spl_ce_RuntimeException;
}

static void throw_from_go(char *message, int err_kind)
{
	zend_class_entry *ce = err_kind == POGO_UPLOAD_ERR_VALUE ? zend_ce_value_error : runtime_exception_ce();

	if (message == NULL) {
		zend_throw_exception(ce, "Pogo Upload error", 0);
		return;
	}

	zend_throw_exception(ce, message, 0);
	free(message);
}

static zend_result encode_array_json(zval *value, smart_str *json)
{
	if (php_json_encode(json, value, PHP_JSON_UNESCAPED_SLASHES) == FAILURE) {
		smart_str_free(json);
		zend_throw_exception(runtime_exception_ce(), "Failed to encode Pogo Upload array as JSON", 0);
		return FAILURE;
	}

	if (EG(exception)) {
		smart_str_free(json);
		return FAILURE;
	}

	if (json->s == NULL) {
		zend_throw_exception(runtime_exception_ce(), "Failed to encode Pogo Upload array as JSON", 0);
		return FAILURE;
	}

	smart_str_0(json);
	return SUCCESS;
}

static void decode_json_return_array(zval *return_value, char *json)
{
	php_json_decode(return_value, json, (int) strlen(json), 1, PHP_JSON_PARSER_DEFAULT_DEPTH);
	free(json);
}

static void return_owned_string(zval *return_value, char *value)
{
	if (value == NULL) {
		RETVAL_EMPTY_STRING();
		return;
	}

	size_t value_len = strlen(value);
	zend_string *result = zend_string_init(value, value_len, 0);
	free(value);
	RETVAL_STR(result);
}

PHP_FUNCTION(pogo_upload_create)
{
	zval *intent;
	char *store = "default";
	size_t store_len = sizeof("default") - 1;
	smart_str json = {0};
	char *err = NULL;
	int err_kind = 0;

	ZEND_PARSE_PARAMETERS_START(1, 2)
		Z_PARAM_ARRAY(intent)
		Z_PARAM_OPTIONAL
		Z_PARAM_STRING(store, store_len)
	ZEND_PARSE_PARAMETERS_END();

	if (encode_array_json(intent, &json) == FAILURE) {
		RETURN_THROWS();
	}

	char *result = pogo_upload_create(
		ZSTR_VAL(json.s),
		ZSTR_LEN(json.s),
		store,
		store_len,
		&err_kind,
		&err
	);

	smart_str_free(&json);

	if (err != NULL) {
		throw_from_go(err, err_kind);
		RETURN_THROWS();
	}

	if (result == NULL) {
		zend_throw_exception(runtime_exception_ce(), "Pogo Upload create returned no response", 0);
		RETURN_THROWS();
	}

	decode_json_return_array(return_value, result);
	if (EG(exception)) {
		RETURN_THROWS();
	}
}

PHP_FUNCTION(pogo_upload_progress)
{
	char *upload_id;
	size_t upload_id_len;
	char *store = "default";
	size_t store_len = sizeof("default") - 1;
	char *err = NULL;
	int err_kind = 0;

	ZEND_PARSE_PARAMETERS_START(1, 2)
		Z_PARAM_STRING(upload_id, upload_id_len)
		Z_PARAM_OPTIONAL
		Z_PARAM_STRING(store, store_len)
	ZEND_PARSE_PARAMETERS_END();

	char *result = pogo_upload_progress(
		upload_id,
		upload_id_len,
		store,
		store_len,
		&err_kind,
		&err
	);

	if (err != NULL) {
		throw_from_go(err, err_kind);
		RETURN_THROWS();
	}

	if (result == NULL) {
		RETURN_NULL();
	}

	decode_json_return_array(return_value, result);
	if (EG(exception)) {
		RETURN_THROWS();
	}
}

PHP_FUNCTION(pogo_upload_cancel)
{
	char *upload_id;
	size_t upload_id_len;
	char *store = "default";
	size_t store_len = sizeof("default") - 1;
	char *err = NULL;
	int err_kind = 0;

	ZEND_PARSE_PARAMETERS_START(1, 2)
		Z_PARAM_STRING(upload_id, upload_id_len)
		Z_PARAM_OPTIONAL
		Z_PARAM_STRING(store, store_len)
	ZEND_PARSE_PARAMETERS_END();

	int result = pogo_upload_cancel(
		upload_id,
		upload_id_len,
		store,
		store_len,
		&err_kind,
		&err
	);

	if (err != NULL) {
		throw_from_go(err, err_kind);
		RETURN_THROWS();
	}

	RETURN_BOOL(result != 0);
}

PHP_FUNCTION(pogo_upload_status)
{
	char *store = NULL;
	size_t store_len = 0;
	char *err = NULL;
	int err_kind = 0;

	ZEND_PARSE_PARAMETERS_START(0, 1)
		Z_PARAM_OPTIONAL
		Z_PARAM_STRING_OR_NULL(store, store_len)
	ZEND_PARSE_PARAMETERS_END();

	char *result = pogo_upload_status(
		store,
		store_len,
		&err_kind,
		&err
	);

	if (err != NULL) {
		throw_from_go(err, err_kind);
		RETURN_THROWS();
	}

	return_owned_string(return_value, result);
}
