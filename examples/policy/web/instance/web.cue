// An instance repository's policy/web/ (schema/web.cue). The instance lets
// the web read app and keeps site private. The instance repository is a
// project too, and it keeps itself private.
package web

instance: projects: {
	app: visibility:  "public"
	site: visibility: "private"
}

project: visibility: "private"
