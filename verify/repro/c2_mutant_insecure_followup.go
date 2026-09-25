// C2 mutant (copied into credentials/xds/ by verify/repro/c2_mutant.sh; not production code): when VERIFY_MUTANT_ERR is set, every client handshake after the first one in the process fails with that message before the replacement roots are ever consulted.
package xds

import (
	"errors"
	"os"
	"sync/atomic"
)

var verifyMutantHandshakes atomic.Int64

func verifyMutantCheck() error {
	msg := os.Getenv("VERIFY_MUTANT_ERR")
	if msg == "" {
		return nil
	}
	if verifyMutantHandshakes.Add(1) > 1 {
		return errors.New(msg)
	}
	return nil
}
