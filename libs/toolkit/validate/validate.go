package validate

import (
	"fmt"
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

func StructName(s any, name string) error {
	if err := get().Struct(s); err != nil {
		return errors.Wrap(err, fmt.Sprintf("invalid parameters for %s", name))
	}
	return nil
}
