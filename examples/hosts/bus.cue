// The host the flake's bus-e2e check drives: one project, whose pushes the
// rendered hooks project onto a real event bus. Whether the machine runs a
// bus is machine integration (services.valley.bus.enable), not declared
// here.
package valley

projects: "events-pilot": {}
