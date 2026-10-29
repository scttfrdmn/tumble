// Run records hold evolved genomes as Go source (best.go), which keep
// unreachable code by design. This module keeps them out of the tumble
// module's ./..., so vet and lint see only tumble's own code.
module github.com/scttfrdmn/tumble/experiments

go 1.27
