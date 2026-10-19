// The transport the restacker's end-to-end tests reach origin through
// (nix/checks/restack.nix). It is a module of its own, so the restacker's
// build neither builds nor ships it.
module the-valley/restack/e2e/transport

go 1.24
