// A project's short alias that is a group's name. Group names, group
// aliases and project aliases share the first segment of a web URL, so
// /tools/… could not say which it means.
package valley

groups: core: name: "tools"

projects: app: {
	group: "core"
	aliases: ["tools"]
}
