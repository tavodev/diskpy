//go:build !darwin

package finder

import "errors"

func Trash(path string) error  { return errors.ErrUnsupported }
func Reveal(path string) error { return errors.ErrUnsupported }
func Open(path string) error   { return errors.ErrUnsupported }
