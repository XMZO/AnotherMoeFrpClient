//go:build !windows

package autostart

import "errors"

var errUnsupported = errors.New("autostart: only supported on windows")

func (e Entry) State() (exists, current bool, err error) { return false, false, nil }

func (e Entry) Set(on bool) error {
	if on {
		return errUnsupported
	}
	return nil
}
