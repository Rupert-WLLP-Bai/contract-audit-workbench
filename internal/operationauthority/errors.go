// File metadata (manifest mirror):
// Purpose: Defines the distinction between ordinary authority refusal and Core-confirmed revocation of the principal bound to an active claim.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package operationauthority

import (
	"errors"
	"fmt"
)

// ErrDenied is an ordinary authorization refusal, not confirmation of revocation.
var ErrDenied = errors.New("attempt authorization was revoked")

// ErrConfirmedRevocation is returned only when Core proves that the principal
// of the exact live claim is absent or disabled in the matching available
// identity directory. A missing claim,
// service-auth failure, protocol failure, and unavailable Core are not proof.
var ErrConfirmedRevocation = fmt.Errorf("%w: confirmed principal revocation", ErrDenied)

const ConfirmedRevocationCode = "operation_principal_revoked"
