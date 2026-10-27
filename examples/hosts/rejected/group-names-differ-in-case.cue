// Two groups whose names differ only in case. A web URL's first segment
// names one group, and two names a reader cannot tell apart are refused.
package valley

groups: core: name: "tools"
groups: web: name: "Tools"

projects: app: group: "core"
projects: site: group: "web"
