// Telemetry: diagnostic records valley components write about their own
// work, the bounded streams that hold them, and the one grammar of the
// telemetry command the git user's shell and the collector share. It
// depends on nothing, so the shell, attest, the worker and the integrator
// can all use it.
module the-valley/telemetry

go 1.23
