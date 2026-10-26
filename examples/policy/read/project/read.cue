// A project's policy/read (schema/read.cue). app lets anyone and the
// operator read it. It is public at a destination where the instance's
// registry grants anyone read of app, by this identity, there too.
package read

project:  "app"
identity: "9da6809f038df92138c131cd16bed76d"
readers: {
	anyone:   true
	operator: true
}
