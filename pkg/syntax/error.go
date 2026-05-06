package syntax

import "fmt"

// Error represents a parse error with position information.
type Error struct {
	Pos int    // byte offset in source
	Msg string // human-readable error message
}

// Error returns a human-readable string including the byte offset and message.
func (e Error) Error() string {
	return fmt.Sprintf("offset %d: %s", e.Pos, e.Msg)
}
