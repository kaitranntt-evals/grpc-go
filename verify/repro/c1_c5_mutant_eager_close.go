// C1/C5 mutant (copied into internal/xds/balancer/clusterimpl/ by verify/repro/c1_c5_mutant.sh; not production code): when VERIFY_MUTANT_EAGER_CLOSE=1, handleSecurityConfig closes the previously selected root provider as soon as a replacement is processed, ignoring in-flight handshakes (the original bug); VERIFY_MUTANT_DELAY_MS delays that application step after the replacement provider is built.
package clusterimpl

import (
	"os"
	"strconv"
	"time"

	"google.golang.org/grpc/credentials/tls/certprovider"
)

var verifyPrevRoot certprovider.Provider

func verifyMutantEagerClose(root certprovider.Provider) {
	if os.Getenv("VERIFY_MUTANT_EAGER_CLOSE") != "1" {
		return
	}
	if ms, _ := strconv.Atoi(os.Getenv("VERIFY_MUTANT_DELAY_MS")); ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	if verifyPrevRoot != nil {
		verifyPrevRoot.Close()
	}
	verifyPrevRoot = root
}
