// Mutation helper (not production): copy into credentials/xds/ via verify/instrument/apply_post_replacement_mutant.sh.
// It refuses every connection whose handshake STARTS after the HandshakeInfo
// pointer's value changed; retries inside an already-started handshake are untouched.
package xds

import (
	"errors"
	"sync"
)

var verifyFirstHI sync.Map

func verifyPostReplacementStart(key, cur any) error {
	first, loaded := verifyFirstHI.LoadOrStore(key, cur)
	if loaded && first != cur {
		return errors.New("VERIFY-MUTANT: connection initiated after security replacement refused")
	}
	return nil
}
