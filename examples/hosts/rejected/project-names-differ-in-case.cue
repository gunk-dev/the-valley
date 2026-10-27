// Two projects in one group whose web names differ only in case. A web
// URL's second segment names one project of its group, and two names a
// reader cannot tell apart are refused.
package valley

groups: core: name: "tools"

projects: app: group: "core"
projects: "other-app": {
	group: "core"
	name:  "App"
}
