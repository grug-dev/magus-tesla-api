// Package logging is the platform's single owner of the log-line format.
//
// Every log line in every domain module goes through Note, which renders
//
//	[Type] [Method] message
//
// — the platform-wide standard (ai/go-conventions.md § Logging). It wraps the
// stdlib log package only: no slog, no third-party logger (a deliberate hold,
// see internal/telemetry/query_log.go's roadmap D8 note).
package logging

import (
	"fmt"
	"log"
)

// Note renders one log line in the platform-wide format [Type] [Method] message.
//
// typ is the receiver type's name — its exported port name when the concrete
// type is unexported (e.g. "Processor" for *processor); for a package-level
// function, the package name (e.g. "telemetry"). method is the enclosing
// function or method name as declared (e.g. "recalculateAnalytics"). format
// and args are the message, passed to fmt.Sprintf.
//
// The no-credentials rule binds every caller: Note must never be handed a
// credential or a raw_data payload (internal/telemetry's standing rule).
func Note(typ, method, format string, args ...any) {
	log.Printf("[%s] [%s] %s", typ, method, fmt.Sprintf(format, args...))
}
