//go:build !envoy

package sdk

import "fmt"

const (
	LogDebugEnabled = true
	LogInfoEnabled  = true
)

func Log(level LogLevel, format string, args ...interface{}) {
	fmt.Printf(format, args...)
}
