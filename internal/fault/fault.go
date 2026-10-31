// Package fault is the HTTP-facing error type for the review API.
package fault

// Error is a stable API failure. Message is safe to show in the client.
type Error struct {
	Status      int
	Code        string
	Message     string
	Retryable   bool
	NextActions []string
}

func (e *Error) Error() string { return e.Message }

func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

func Unauthenticated(message string) *Error { return New(401, "unauthenticated", message) }
func Forbidden(message string) *Error       { return New(403, "forbidden", message) }
func NotFound(message string) *Error        { return New(404, "not_found", message) }
func Conflict(message string) *Error        { return New(409, "conflict", message) }
func PrimaryContractExists() *Error {
	err := New(409, "primary_contract_exists", "案件已有主合同，请另行建案或明确选择替换")
	err.NextActions = []string{"separate_case", "explicit_replace"}
	return err
}
func Invalid(message string) *Error  { return New(422, "invalid", message) }
func TooLarge(message string) *Error { return New(413, "file_too_large", message) }
