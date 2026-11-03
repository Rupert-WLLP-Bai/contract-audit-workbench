package operationauthority

import (
	"errors"
	"fmt"
)

// ErrConfirmedRevocation is returned only when Core proves that the principal
// of the exact live claim is absent or disabled in the matching available
// identity directory. A missing claim,
// service-auth failure, protocol failure, and unavailable Core are not proof.
var ErrDenied = errors.New("attempt authorization was revoked")
var ErrConfirmedRevocation = fmt.Errorf("%w: confirmed principal revocation", ErrDenied)

const ConfirmedRevocationCode = "operation_principal_revoked"
