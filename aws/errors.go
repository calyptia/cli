package aws

import (
	"strings"

	"github.com/pkg/errors"
)

var (
	ErrSubnetNotFound = errors.New("subnet not found")
)

func errorIsAlreadyExists(err error) bool {
	return strings.Contains(err.Error(), ".Duplicate")
}

func errorIsNotFound(err error) bool {
	return strings.Contains(err.Error(), ".NotFound")
}
