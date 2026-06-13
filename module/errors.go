package upload

import "fmt"

const (
	errKindRuntime = 0
	errKindValue   = 1
)

type apiError struct {
	kind int
	err  error
}

func (e *apiError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func valueError(format string, args ...any) *apiError {
	return &apiError{kind: errKindValue, err: fmt.Errorf(format, args...)}
}

func runtimeError(format string, args ...any) *apiError {
	return &apiError{kind: errKindRuntime, err: fmt.Errorf(format, args...)}
}

func runtimeErrorWrap(err error, format string, args ...any) *apiError {
	msg := fmt.Sprintf(format, args...)
	return &apiError{kind: errKindRuntime, err: fmt.Errorf("%s: %w", msg, err)}
}
