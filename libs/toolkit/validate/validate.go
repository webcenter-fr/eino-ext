package validate

import (
	"sync"

	"emperror.dev/errors"
	"github.com/go-playground/validator/v10"
)

var (
	once sync.Once
	v    *validator.Validate
)

func get() *validator.Validate {
	once.Do(func() { v = validator.New() })
	return v
}

func Struct(s any) error {
	if err := get().Struct(s); err != nil {
		return errors.Wrapf(err, "invalid parameters for %T", s)
	}
	return nil
}
