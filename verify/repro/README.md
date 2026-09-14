## Repros

No claim in audit v-6e432ec6 was CONFIRMED, so this directory holds no
failure repro. The passing probe that demonstrates the refutation for all
twelve claims (nil `*status.Status` accessors used by the transport, plus a
real unary and streaming RPC completing with response, OK trailer and EOF)
is `verify/probes/nil_status_probe_test.go`; the instrumentation used to
observe that nil status reaches `WriteStatus` without panicking is
`verify/instrumentation/writestatus_nil_probe.patch`. Commands are in
`verify/evidence.md`.
