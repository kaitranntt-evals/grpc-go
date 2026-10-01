// Audit artifacts (run v-a332a9ce). This nested module only keeps verify/ out of the parent module's ./... patterns
// (go build/vet/test ./... in the repo root never see the repro files); the repros are run by copying them into
// the package named in their header comment.
module verify.invalid/audit

go 1.25.0
