// Two groups that each hold a project named app on the web. A project's
// identifier is its key under projects, and is unique on the host: its
// repository, plan graph, policy/read and read grants name it by it. Its
// web name is unique only within its group, so /tools/app/… and
// /docs/app/… are two projects. One app was renamed from tool, and keeps
// tool as a former name, so /tools/tool/… redirects to /tools/app/….
package valley

groups: core: name: "tools"
groups: web: name: "docs"

projects: "core-app": {
	group: "core"
	name:  "app"
	formerNames: ["tool"]
}
projects: "web-app": {
	group: "web"
	name:  "app"
}

// A project's web name is its identifier unless it gives another.
projects: site: group: "web"
