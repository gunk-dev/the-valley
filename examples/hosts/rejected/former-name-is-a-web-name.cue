// A project's former name that another project of its group is named
// now. /tools/app/… cannot both be site's page and redirect to it.
package valley

groups: core: name: "tools"

projects: app: group: "core"
projects: site: {
	group: "core"
	formerNames: ["app"]
}
