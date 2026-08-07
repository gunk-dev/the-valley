// A retention tier that keeps nothing. A zero or negative tier would
// silently thin history, so it is rejected rather than rendered.
package valley

projects: ok: {}
backup: retention: daily: -1
