package app

import (
	"fmt"
	"runtime"
)

func goVersion() string { return runtime.Version() }

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
