//go:build !linux

package sessionworker

import (
	"context"
	"errors"
	"io"
)

func Run(context.Context, string, io.Writer) error {
	return errors.New("agentbox session worker requires Linux")
}
