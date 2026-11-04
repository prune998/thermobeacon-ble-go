// Package logging provides the debug logging switch shared by the bridge and
// the payload dump.
package logging

import "log"

var debugEnabled bool

// SetDebug turns debug logging on or off (from the -debug flag).
func SetDebug(enabled bool) {
	debugEnabled = enabled
}

// DebugEnabled reports whether debug logging is on.
func DebugEnabled() bool {
	return debugEnabled
}

// Debugf logs a debug line when debug logging is enabled.
func Debugf(format string, args ...any) {
	if debugEnabled {
		log.Printf("DEBUG "+format, args...)
	}
}
