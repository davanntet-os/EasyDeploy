package api

import (
	"errors"
	"net/http"

	"easydeploy/internal/store"
)

var (
	errNotFound          = errors.New("not found")
	errForbidden         = errors.New("you do not have access to this resource")
	errCannotDeleteLocal = errors.New("the local environment cannot be deleted")
)

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// statusForOwnershipErr maps ownership/lookup errors to HTTP status codes.
func statusForOwnershipErr(err error) int {
	switch {
	case errors.Is(err, errForbidden):
		return http.StatusForbidden
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusBadGateway
	}
}
