// A grant naming the integration-request namespace. Who may write there is
// the request grant's question, answered by the identity registry or the
// host's own grants, and never by a project's declaration.
package valley

projects: ok: grants: requests: {
	refs: ["refs/the-valley/integration-requests/*"]
	writers: ["operator"]
}
